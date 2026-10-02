package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"specter/internal/ats"
	"specter/internal/crawler"
	"specter/internal/exporter"
	"specter/internal/reporter"
	"specter/internal/seeds"
	"specter/internal/signals"
	"specter/internal/storage"
	"specter/internal/sync"
	"specter/internal/ui"

	"golang.org/x/sync/errgroup"
)

const version = "1.3.0"

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "scan":
		runScan(os.Args[2:])
	case "sync":
		runSync(os.Args[2:])
	case "apply":
		runApply(os.Args[2:])
	case "leads":
		runLeads(os.Args[2:])
	case "roles":
		runRoles(os.Args[2:])
	case "companies":
		runCompanies(os.Args[2:])
	case "report":
		runReport(os.Args[2:])
	case "export":
		runExport(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("Specter CLI v%s\n", version)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n\n", command)
		printHelp()
		os.Exit(1)
	}
}

func runScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	all := fs.Bool("all", false, "Scan all targets from curated seed catalog")
	seedsPath := fs.String("seeds", "", "Path to custom seeds JSON file (defaults to embedded seeds)")
	limit := fs.Int("limit", 0, "Limit number of seed targets to process (0 = all)")
	concurrency := fs.Int("concurrency", 4, "Number of concurrent worker goroutines")
	target := fs.String("target", "", "Target ATS URL, board token, or company domain (e.g. boards.greenhouse.io/stripe)")
	githubOrg := fs.String("github", "", "GitHub organization name to mine contributor leads (e.g. stripe)")
	tokensFlag := fs.String("tokens", "", "Comma-separated GitHub tokens for round-robin rotation (or GITHUB_TOKENS env)")
	roleFilter := fs.String("role", "backend", "Target role focus (default: backend)")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	outDir := fs.String("out-dir", "reports", "Directory to write Markdown reports")
	ttlDays := fs.Int("ttl", 7, "Frontier deduplication TTL in days (re-crawl if older)")
	timeoutSec := fs.Int("timeout", 15, "HTTP request timeout in seconds")
	syncSheets := fs.Bool("sync-sheets", false, "Automatically sync newly discovered leads to Google Sheets upon completion")
	webhookURL := fs.String("webhook", "", "Custom Google Sheets webhook URL override")

	fs.Parse(args)

	if !*all && *target == "" {
		fmt.Fprintln(os.Stderr, "Error: specify --all to run the curated catalog, or provide --target=<ats_or_domain>")
		fmt.Fprintln(os.Stderr, "Example: specter scan --all --concurrency 4")
		os.Exit(1)
	}

	// 1. Root context with OS Interrupt (Ctrl+C) handling
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed opening database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// 2. Initialize persistent Frontier
	frontier, err := crawler.NewFrontier(store.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed initializing crawl frontier: %v\n", err)
		os.Exit(1)
	}

	// GitHub token resolution and pool initialization
	ghTokens := *tokensFlag
	if ghTokens == "" {
		ghTokens = os.Getenv("GITHUB_TOKENS")
	}
	if ghTokens == "" {
		ghTokens = os.Getenv("GITHUB_TOKEN")
	}

	var tp *crawler.TokenPool
	if ghTokens != "" {
		tp = crawler.NewTokenPool(strings.Split(ghTokens, ","))
	} else {
		tp = crawler.NewTokenPoolFromEnv()
	}

	fetcher := crawler.NewFetcher(crawler.FetcherConfig{
		Timeout:      time.Duration(*timeoutSec) * time.Second,
		MaxBodyBytes: 5 * 1024 * 1024,
		TokenPool:    tp,
		Frontier:     frontier,
	})

	registry := ats.NewRegistry(fetcher)
	crawlTTL := time.Duration(*ttlDays) * 24 * time.Hour

	miner := signals.NewGitMiner(fetcher, signals.GitMinerOptions{
		GitHubTokens:   ghTokens,
		MaxRepos:       15,
		MaxCommitsRepo: 25,
		ExtractPatches: true,
	})

	// 3. Assemble target seeds
	var targetSeeds []seeds.SeedTarget
	if *all {
		var sErr error
		targetSeeds, sErr = seeds.LoadSeeds(*seedsPath)
		if sErr != nil {
			fmt.Fprintf(os.Stderr, "Failed loading seeds: %v\n", sErr)
			os.Exit(1)
		}
	} else if *target != "" {
		dom := extractDomainName(*target)
		atsService, boardID := detectATSTypeAndBoard(*target)
		targetSeeds = []seeds.SeedTarget{
			{
				Name:       dom,
				Domain:     dom,
				GitHubOrg:  *githubOrg,
				ATSService: atsService,
				ATSBoardID: boardID,
				Priority:   1,
			},
		}
	}

	if *limit > 0 && len(targetSeeds) > *limit {
		targetSeeds = targetSeeds[:*limit]
	}

	if *concurrency <= 0 {
		*concurrency = 4
	}

	// 4. Initialize RunManager and query previous crawl run
	startTime := time.Now()
	prevRun, _ := store.GetLastCrawlRun(ctx)
	runManager := reporter.NewRunManager(*outDir)
	runID, runDir, rmErr := runManager.InitRunDir(startTime)
	if rmErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed initializing run directory: %v\n", rmErr)
		runDir = *outDir
		runID = startTime.Format("2006-01-02_15-04-05")
	}

	var (
		totalNewRoles atomic.Int64
		totalNewLeads atomic.Int64
	)

	// 5. Initialize real-time non-blocking progress tracker
	tracker := ui.NewProgressTracker(os.Stderr)
	tracker.SetTotalTargets(int64(len(targetSeeds)))
	tracker.SetActiveWorkers(*concurrency)
	tracker.Start(150 * time.Millisecond)
	defer tracker.Stop()

	fmt.Printf("\n⚡ \033[1;36mSPECTER AUTONOMOUS RECON ENGINE v%s\033[0m\n", version)
	fmt.Printf("   Run ID:      \033[1;32m%s\033[0m\n", runID)
	fmt.Printf("   Run Dir:     \033[1m%s\033[0m\n", runDir)
	fmt.Printf("   Targets:     \033[1m%d companies queued\033[0m\n", len(targetSeeds))
	fmt.Printf("   Concurrency: \033[1m%d workers\033[0m\n", *concurrency)
	fmt.Printf("   GitHub Pool: \033[1m%d tokens active\033[0m\n", tp.Count())
	fmt.Printf("   Pacer:       \033[1mAdaptive (1.2 rps GH, 2.0 rps ATS, 300-1200ms jitter, canary 429 recovery)\033[0m\n")
	fmt.Printf("   Filter:      \033[1m%s\033[0m\n", *roleFilter)
	fmt.Printf("   Dedupe TTL:  \033[1m%d days\033[0m\n", *ttlDays)
	fmt.Printf("   Database:    \033[1m%s\033[0m\n\n", *dbPath)

	// Interruption watcher
	go func() {
		<-ctx.Done()
		tracker.SetRateLimitStatus("INTERRUPTED (Draining)")
	}()

	// 6. Concurrent execution using errgroup
	var g errgroup.Group
	g.SetLimit(*concurrency)

	for _, s := range targetSeeds {
		seed := s
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}

			tracker.SetCurrentTarget(seed.ATSBoardID)
			apiURL := formatSeedAPIURL(seed)

			// Frontier Deduplication Check
			shouldCrawlDomain, _ := frontier.ShouldCrawlDomain(seed.Domain, crawlTTL)
			shouldCrawlAPI, _ := frontier.ShouldCrawl(apiURL, crawlTTL)

			if !shouldCrawlDomain && !shouldCrawlAPI {
				tracker.IncCrawled()
				tracker.Log("♻️  [FRONTIER] %s (%s) crawled recently. Skipping.", seed.Name, seed.Domain)
				_, _ = reporter.GenerateMarkdownReport(ctx, store, seed.Domain, runDir)
				return nil
			}

			// A. Ingest ATS
			meta, err := registry.ExtractBySeed(ctx, seed)
			_ = frontier.MarkVisited(apiURL, "ats_"+seed.ATSService, err)
			tracker.IncCrawled()

			if err != nil {
				tracker.Log("⚠️  ATS extraction error for %s (%s): %v", seed.Name, seed.ATSBoardID, err)
			} else if meta != nil {
				if seed.GitHubOrg != "" && meta.GitHubOrg == "" {
					meta.GitHubOrg = seed.GitHubOrg
				}
				_, newRoles, saveErr := store.SaveCompanyAndRoles(ctx, meta)
				if saveErr != nil {
					tracker.Log("⚠️  Database write error for %s: %v", seed.Name, saveErr)
				} else {
					totalNewRoles.Add(int64(newRoles))
					tracker.Log("✅ %s: %d backend roles matching signals (%d new)", seed.Name, len(meta.OpenRoles), newRoles)
				}
			}

			if meta == nil {
				meta = &ats.CompanyMeta{
					Name:      seed.Name,
					Domain:    seed.Domain,
					GitHubOrg: seed.GitHubOrg,
				}
			}

			// B. Git Contributor Mining
			targetOrg := seed.GitHubOrg
			if targetOrg == "" && meta.GitHubOrg != "" {
				targetOrg = meta.GitHubOrg
			}

			if targetOrg != "" && ctx.Err() == nil {
				gitKey := "github:" + targetOrg
				shouldMine, _ := frontier.ShouldCrawl(gitKey, crawlTTL)

				if !shouldMine {
					tracker.Log("♻️  [FRONTIER] GitHub org @%s visited recently.", targetOrg)
				} else {
					leads, mineErr := miner.MineOrganization(ctx, targetOrg, seed.Domain, nil)
					_ = frontier.MarkVisited(gitKey, "github_org", mineErr)

					if mineErr != nil {
						tracker.Log("⚠️  GitHub mining warning for @%s: %v", targetOrg, mineErr)
					} else {
						validEmails := 0
						for _, l := range leads {
							if l.Email != "" {
								validEmails++
							}
						}
						newLeads, _ := store.SaveEngineeringLeads(ctx, leads)
						totalNewLeads.Add(int64(newLeads))
						tracker.IncLeads(len(leads))
						tracker.IncEmails(validEmails)
						if len(leads) > 0 {
							tracker.Log("🎯 @%s: Discovered %d leads (%d verified emails, %d new saved)",
								targetOrg, len(leads), validEmails, newLeads)
						}
					}
				}
			}

			// C. Markdown Dossier Compilation into runDir
			reportCtx, reportCancel := context.WithTimeout(context.Background(), 3*time.Second)
			reportPath, rErr := reporter.GenerateMarkdownReport(reportCtx, store, seed.Domain, runDir)
			reportCancel()
			if rErr == nil && reportPath != "" {
				tracker.Log("📄 Saved dossier: %s", reportPath)
			}

			return nil
		})
	}

	_ = g.Wait()

	// 7. Graceful Interruption Drain
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\n\n🛑 \033[1;33m[SHUTDOWN] Interruption caught (Ctrl+C). Draining in-flight workers (max 3s)...\033[0m")
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer drainCancel()

		fmt.Fprintln(os.Stderr, "💾 Committing pending state to specter.db...")
		for _, s := range targetSeeds {
			if path, err := reporter.GenerateMarkdownReport(drainCtx, store, s.Domain, runDir); err == nil && path != "" {
				fmt.Fprintf(os.Stderr, "📄 Preserved dossier: %s\n", path)
			}
		}
		if sPath, sErr := reporter.GenerateSourcesReport(drainCtx, store, miner.GetYieldStats(), runDir); sErr == nil && sPath != "" {
			fmt.Fprintf(os.Stderr, "📊 Preserved source analytics: %s\n", sPath)
		}
		if *syncSheets {
			drainSyncCtx, drainSyncCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer drainSyncCancel()
			sheetsClient := sync.NewSheetsClient(*webhookURL)
			_, _ = sheetsClient.SyncLeads(drainSyncCtx, store, sync.SyncOptions{
				Limit: 100,
				LogFunc: func(format string, a ...any) {
					fmt.Fprintf(os.Stderr, format, a...)
				},
			})
		}
		fmt.Fprintln(os.Stderr, "👋 Specter terminated gracefully. State safely preserved in specter.db.")
		os.Exit(130)
	}

	tracker.Stop()

	completedTime := time.Now()
	allCompanies, _ := store.ListCompanies(context.Background())
	allRoles, _ := store.ListRoles(context.Background(), "")
	allLeads, _ := store.ListEngineeringLeads(context.Background(), "", false)

	// Persist Crawl Run metadata in SQLite
	currentRun := &storage.CrawlRun{
		RunID:          runID,
		StartedAt:      startTime,
		CompletedAt:    completedTime,
		TotalCompanies: len(allCompanies),
		TotalRoles:     len(allRoles),
		NewRoles:       int(totalNewRoles.Load()),
		TotalLeads:     len(allLeads),
		NewLeads:       int(totalNewLeads.Load()),
		ReportDir:      runDir,
	}
	_ = store.RecordCrawlRun(context.Background(), currentRun)

	// Fetch direct apply roles and isolate brand-new roles
	directRoles, _ := store.ListDirectApplyRoles(context.Background(), storage.DirectApplyFilter{})
	var freshRoles []ats.JobPosting
	for _, r := range directRoles {
		if r.IsNew || (!r.FirstSeenAt.IsZero() && !r.FirstSeenAt.Before(startTime)) {
			freshRoles = append(freshRoles, r)
		}
	}

	var freshLeads []signals.EngineeringLead
	for _, l := range allLeads {
		if !l.DiscoveredAt.Before(startTime) {
			freshLeads = append(freshLeads, l)
		}
	}

	// Generate Aggregated Reports in runDir
	sourcesPath, sErr := reporter.GenerateSourcesReport(context.Background(), store, miner.GetYieldStats(), runDir)
	if sErr == nil && sourcesPath != "" {
		fmt.Printf("📊 \033[1;32mAggregated Sources Analytics:\033[0m %s\n", sourcesPath)
	}

	directApplyPath, _, dErr := reporter.GenerateDirectApplyReport(directRoles, runDir)
	if dErr == nil && directApplyPath != "" {
		fmt.Printf("🎯 \033[1;32mDirect One-Click Apply Dashboard:\033[0m %s\n", directApplyPath)
	}

	deltaPath, _, deltaErr := reporter.GenerateDeltaReport(currentRun, prevRun, freshRoles, freshLeads, runDir)
	if deltaErr == nil && deltaPath != "" {
		fmt.Printf("🔄 \033[1;32mRecon Delta Summary:\033[0m %s\n", deltaPath)
	}

	// Link latest run directory pointer
	_ = runManager.LinkLatestRun(runDir)
	fmt.Printf("📁 \033[1;34mRun Directory Archive:\033[0m %s (\033[2mlinked at %s/latest\033[0m)\n", runDir, *outDir)

	// Dispatch Google Sheets synchronization if requested
	if *syncSheets {
		fmt.Println()
		sheetsClient := sync.NewSheetsClient(*webhookURL)
		_, _ = sheetsClient.SyncLeads(context.Background(), store, sync.SyncOptions{
			Limit: 200,
			LogFunc: func(format string, a ...any) {
				fmt.Printf(format, a...)
			},
		})
	}

	printCatalogSummary(store)
}

func formatSeedAPIURL(seed seeds.SeedTarget) string {
	switch strings.ToLower(seed.ATSService) {
	case "greenhouse":
		return fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs", seed.ATSBoardID)
	case "lever":
		return fmt.Sprintf("https://api.lever.co/v0/postings/%s", seed.ATSBoardID)
	case "ashby":
		return fmt.Sprintf("https://api.ashbyhq.com/posting-api/job-board/%s", seed.ATSBoardID)
	default:
		return "https://" + seed.Domain
	}
}

func detectATSTypeAndBoard(target string) (string, string) {
	lower := strings.ToLower(target)
	parts := strings.Split(strings.Trim(target, "/"), "/")

	if strings.Contains(lower, "greenhouse.io") {
		if len(parts) > 1 {
			return "greenhouse", parts[len(parts)-1]
		}
		return "greenhouse", target
	}
	if strings.Contains(lower, "lever.co") {
		if len(parts) > 1 {
			return "lever", parts[len(parts)-1]
		}
		return "lever", target
	}
	if strings.Contains(lower, "ashbyhq.com") {
		if len(parts) > 1 {
			return "ashby", parts[len(parts)-1]
		}
		return "ashby", target
	}
	return "generic", target
}

func printCatalogSummary(store *storage.Store) {
	ctx := context.Background()
	companies, _ := store.ListCompanies(ctx)
	roles, _ := store.ListRoles(ctx, "")
	globalRoles, _ := store.ListDirectApplyRoles(ctx, storage.DirectApplyFilter{GlobalOnly: true})
	leads, _ := store.ListEngineeringLeads(ctx, "", false)

	fmt.Println()
	fmt.Printf("═══════════════════════════════════════════════════════════════════\n")
	fmt.Printf(" 🎯 SPECTER AUTONOMOUS RUN COMPLETE\n")
	fmt.Printf("═══════════════════════════════════════════════════════════════════\n")
	fmt.Printf(" Total Companies Scanned:       %d\n", len(companies))
	fmt.Printf(" Active Open Backend Roles:     %d\n", len(roles))
	fmt.Printf(" 🟢 Global / Contractor Roles:  %d (Direct One-Click Apply)\n", len(globalRoles))
	fmt.Printf(" Verified Engineering Leads:    %d\n\n", len(leads))

	if len(leads) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "SCORE\tNAME\tARCHETYPE\tEMAIL\tGITHUB\tTOP LANGUAGES")
		for i, l := range leads {
			if i >= 15 {
				fmt.Fprintf(w, "... and %d more leads recorded in specter.db\n", len(leads)-15)
				break
			}
			gh := "-"
			if l.GitHubHandle != "" {
				gh = "@" + l.GitHubHandle
			}
			roleDisplay := l.Role
			if roleDisplay == "" {
				roleDisplay = "Core Contributor"
			}
			fmt.Fprintf(w, "%d/100\t%s\t%s\t%s\t%s\t%s\n",
				l.RelevanceScore, l.Name, roleDisplay, l.Email, gh, l.TopLanguages)
		}
		w.Flush()
	}
	fmt.Printf("\n✨ Dossiers, Direct Apply & Delta Reports saved in ./reports/latest/\n")
	fmt.Printf("   Query direct-apply jobs: `specter apply --global-only`\n\n")
}

func runLeads(args []string) {
	if len(args) == 0 {
		args = []string{"list"}
	}

	subcommand := args[0]
	switch subcommand {
	case "list":
		fs := flag.NewFlagSet("leads list", flag.ExitOnError)
		domain := fs.String("domain", "", "Filter by company domain")
		uncontacted := fs.Bool("uncontacted", false, "Show only uncontacted leads")
		dbPath := fs.String("db", "specter.db", "SQLite database path")
		fs.Parse(args[1:])

		store, err := storage.NewStore(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		ctx := context.Background()
		engLeads, err := store.ListEngineeringLeads(ctx, *domain, *uncontacted)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying leads: %v\n", err)
			os.Exit(1)
		}

		if len(engLeads) == 0 {
			fmt.Println("No leads found matching criteria. Run `specter scan --all` or `specter scan --target=<url>` first.")
			return
		}

		fmt.Printf("\n📋 \033[1mDISCOVERED ENGINEERING LEADS (%d found)\033[0m\n", len(engLeads))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tSCORE\tNAME\tARCHETYPE\tEMAIL\tGITHUB\tPROVENANCE\tCONTACTED")
		for _, l := range engLeads {
			contactStatus := "No"
			if l.Contacted {
				contactStatus = "Yes"
			}
			gh := "@" + l.GitHubHandle
			if l.GitHubHandle == "" {
				gh = "-"
			}
			roleDisplay := l.Role
			if roleDisplay == "" {
				roleDisplay = "Core Contributor"
			}
			provDisplay := l.RepoName
			if l.CommitSHA != "" {
				c := l.CommitSHA
				if len(c) > 7 {
					c = c[:7]
				}
				if provDisplay != "" {
					provDisplay = c + "@" + provDisplay
				} else {
					provDisplay = c
				}
			}
			if provDisplay == "" {
				provDisplay = l.Source
			}
			fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
				l.ID, l.RelevanceScore, l.Name, roleDisplay, l.Email, gh, provDisplay, contactStatus)
		}
		w.Flush()
		fmt.Println()

	case "contact":
		fs := flag.NewFlagSet("leads contact", flag.ExitOnError)
		id := fs.Int64("id", 0, "Lead ID to mark as contacted")
		dbPath := fs.String("db", "specter.db", "SQLite database path")
		fs.Parse(args[1:])

		if *id <= 0 {
			fmt.Fprintln(os.Stderr, "Error: --id=<lead_id> is required")
			os.Exit(1)
		}

		store, err := storage.NewStore(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		if err := store.MarkEngineeringLeadContacted(context.Background(), *id); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to update lead: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Lead #%d marked as contacted.\n", *id)

	default:
		fmt.Printf("Unknown leads subcommand %q. Use `specter leads list` or `specter leads contact --id=<id>`\n", subcommand)
	}
}

func runReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	domain := fs.String("domain", "", "Company domain to generate dossier for (e.g. stripe.com)")
	outDir := fs.String("out-dir", "reports", "Directory to write Markdown report")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)

	if *domain == "" {
		fmt.Fprintln(os.Stderr, "Error: --domain=<company_domain> is required (e.g. specter report --domain=stripe.com)")
		os.Exit(1)
	}

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx := context.Background()
	path, err := reporter.GenerateMarkdownReport(ctx, store, *domain, *outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed generating report: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Executive Markdown dossier created at: \033[1;32m%s\033[0m\n", path)
}

func runRoles(args []string) {
	fs := flag.NewFlagSet("roles", flag.ExitOnError)
	keyword := fs.String("keyword", "", "Filter roles by keyword (e.g. golang, redis)")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	roles, err := store.ListRoles(context.Background(), *keyword)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying roles: %v\n", err)
		os.Exit(1)
	}

	if len(roles) == 0 {
		fmt.Println("No open roles found.")
		return
	}

	fmt.Printf("\n💼 \033[1mACTIVE OPEN BACKEND ROLES (%d found)\033[0m\n", len(roles))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TITLE\tSENIORITY\tLOCATION\tSIGNALS\tURL")
	for _, r := range roles {
		kw := strings.Join(r.Keywords, ", ")
		loc := r.Location
		if loc == "" {
			loc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Title, r.Seniority, loc, kw, r.URL)
	}
	w.Flush()
	fmt.Println()
}

func runCompanies(args []string) {
	fs := flag.NewFlagSet("companies", flag.ExitOnError)
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	companies, err := store.ListCompanies(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying companies: %v\n", err)
		os.Exit(1)
	}

	if len(companies) == 0 {
		fmt.Println("No companies scanned yet.")
		return
	}

	fmt.Printf("\n🏢 \033[1mSCANNED COMPANIES (%d found)\033[0m\n", len(companies))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "COMPANY\tDOMAIN\tCAREERS URL\tGITHUB ORG\tSIGNALS")
	for _, c := range companies {
		sigs := strings.Join(c.Signals, ", ")
		if sigs == "" {
			sigs = "-"
		}
		gh := c.GitHubOrg
		if gh == "" {
			gh = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Domain, c.CareersURL, gh, sigs)
	}
	w.Flush()
	fmt.Println()
}

func runExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "json", "Export format: json or csv")
	output := fs.String("output", "", "Optional file output path (defaults to stdout)")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := exporter.Export(context.Background(), store, *format, *output); err != nil {
		fmt.Fprintf(os.Stderr, "Export failed: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		fmt.Printf("✅ Data successfully exported to %s in %s format.\n", *output, strings.ToUpper(*format))
	}
}

func extractDomainName(target string) string {
	target = strings.TrimPrefix(target, "https://")
	target = strings.TrimPrefix(target, "http://")
	parts := strings.Split(target, "/")
	if len(parts) > 0 {
		host := parts[0]
		if strings.Contains(host, "greenhouse.io") || strings.Contains(host, "lever.co") || strings.Contains(host, "ashbyhq.com") {
			if len(parts) > 1 && parts[1] != "" {
				return parts[1] + ".com"
			}
		}
		return strings.TrimPrefix(host, "www.")
	}
	return "target.com"
}

func runApply(args []string) {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	globalOnly := fs.Bool("global-only", false, "Show only worldwide / contractor-friendly roles (sanction-resilient)")
	contractorOnly := fs.Bool("contractor-only", false, "Show only roles welcoming B2B/independent contractors")
	freshOnly := fs.Bool("fresh-only", false, "Show only roles discovered since the previous crawl run")
	domain := fs.String("domain", "", "Filter roles by company domain")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	outDir := fs.String("out-dir", "reports", "Directory to write Markdown reports")
	exportMD := fs.Bool("export", false, "Generate direct_apply.md report in output directory")

	fs.Parse(args)

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed opening database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx := context.Background()
	var since time.Time
	if *freshOnly {
		lastRun, _ := store.GetLastCrawlRun(ctx)
		if lastRun != nil {
			since = lastRun.StartedAt
		}
	}

	filter := storage.DirectApplyFilter{
		CompanyDomain:  *domain,
		GlobalOnly:     *globalOnly,
		ContractorOnly: *contractorOnly,
		FreshOnly:      *freshOnly,
		Since:          since,
	}

	roles, err := store.ListDirectApplyRoles(ctx, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed querying roles: %v\n", err)
		os.Exit(1)
	}

	if len(roles) == 0 {
		fmt.Println("No matching roles found with the given filter.")
		return
	}

	fmt.Println()
	fmt.Printf("🎯 \033[1;36mSPECTER DIRECT ONE-CLICK APPLY BOARD\033[0m (%d roles found)\n", len(roles))
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════════════════════════════════")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCOMPANY\tROLE TITLE\tPOLICY\tDIRECT APPLY LINK")
	for i, r := range roles {
		if i >= 35 {
			fmt.Fprintf(w, "... and %d more roles in database. Use --export to generate full markdown.\n", len(roles)-35)
			break
		}
		status := "Active"
		if r.IsNew {
			status = "🔥 NEW"
		}
		comp := r.CompanyDomain
		if comp == "" {
			comp = extractDomainName(r.URL)
		}
		applyURL := r.ApplyURL
		if applyURL == "" {
			applyURL = r.URL
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", status, comp, r.Title, r.RemotePolicy, applyURL)
	}
	w.Flush()
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════════════════════════════════")

	if *exportMD {
		path, _, err := reporter.GenerateDirectApplyReport(roles, *outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed generating markdown report: %v\n", err)
		} else {
			fmt.Printf("📄 Exported one-click application dashboard: \033[1;32m%s\033[0m\n", path)
		}
	}
}

func printHelp() {
	fmt.Println(`Specter CLI - Autonomous Recon & Engineering Lead Discovery Engine

Usage:
  specter scan --all [--concurrency=4] [--limit=10] [--ttl=7] [--sync-sheets]
  specter scan --target=<ats_or_domain> [--github=<org>] [--concurrency=4] [--sync-sheets]
  specter sync sheets [--webhook=<url>] [--all] [--limit=100] [--dry-run]
  specter apply [--global-only] [--contractor-only] [--fresh-only] [--domain=<domain>] [--export]
  specter leads list [--domain=<domain>] [--uncontacted]
  specter leads contact --id=<id>
  specter report --domain=<domain> [--out-dir=reports]
  specter roles [--keyword=<kw>]
  specter companies
  specter export [--format=json|csv|roles-csv] [--output=<file>]

Examples:
  specter scan --all --concurrency 4 --sync-sheets
  specter scan --all --limit 5
  specter sync sheets --limit 25
  specter sync sheets --dry-run
  specter sync sheets --all --webhook="https://script.google.com/macros/s/.../exec"
  specter apply --global-only
  specter apply --contractor-only --fresh-only
  specter scan --target=boards.greenhouse.io/stripe --github=stripe
  specter report --domain=canonical.com
  specter leads list --uncontacted
  specter export --format=roles-csv --output=direct_apply.csv`)
}

