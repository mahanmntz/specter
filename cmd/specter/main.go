package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"specter/internal/ats"
	"specter/internal/config"
	"specter/internal/crawler"
	"specter/internal/exporter"
	"specter/internal/feeds"
	"specter/internal/notify"
	"specter/internal/reporter"
	"specter/internal/runner"
	"specter/internal/scanner"
	"specter/internal/seeds"
	"specter/internal/signals"
	"specter/internal/storage"
	"specter/internal/sync"
	"specter/internal/ui"
	"specter/internal/web"

	"golang.org/x/sync/errgroup"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "1.3.0"

// nonInteractive suppresses prompts (set by unattended modes such as `watch`).
var nonInteractive bool

func main() {
	// Auto-load .env configuration file if present
	_ = config.LoadDotEnv()

	// Optional user profile: SPECTER_PROFILE, or ./profile.json when present.
	profilePath := os.Getenv("SPECTER_PROFILE")
	if profilePath == "" {
		if _, err := os.Stat("profile.json"); err == nil {
			profilePath = "profile.json"
		}
	}
	if profilePath != "" {
		if err := signals.LoadProfile(profilePath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed loading profile: %v\n", err)
			os.Exit(1)
		}
	}

	if len(os.Args) < 2 {
		printHelp()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "run":
		runAutonomous(os.Args[2:])
	case "scan":
		runScan(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "watch":
		runWatch(os.Args[2:])
	case "feeds":
		runFeeds(os.Args[2:])
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

// runAutonomous executes the full scan and automatically synchronizes to Google Sheets.
func runAutonomous(args []string) {
	var scanArgs []string
	hasForce := false
	hasSync := false
	hasLimit := false

	for _, a := range args {
		if strings.HasPrefix(a, "--force") || strings.HasPrefix(a, "-force") {
			hasForce = true
		}
		if strings.HasPrefix(a, "--sync-sheets") {
			hasSync = true
		}
		if strings.HasPrefix(a, "--limit") {
			hasLimit = true
		}
		scanArgs = append(scanArgs, a)
	}

	if !hasForce {
		scanArgs = append(scanArgs, "--force")
	}
	if !hasSync {
		scanArgs = append(scanArgs, "--sync-sheets")
	}
	if !hasLimit && len(args) == 0 {
		// Default to running all seeds when invoked simply as `specter run`
		scanArgs = append(scanArgs, "--all")
	}

	runScan(scanArgs)
}

func runScan(args []string) {
	defaultConcurrency := 4
	if envC := os.Getenv("SPECTER_CONCURRENCY"); envC != "" {
		if c, err := strconv.Atoi(envC); err == nil && c > 0 {
			defaultConcurrency = c
		}
	}
	defaultTTL := 3
	if envTTL := os.Getenv("SPECTER_CRAWL_TTL_DAYS"); envTTL != "" {
		if t, err := strconv.Atoi(envTTL); err == nil && t > 0 {
			defaultTTL = t
		}
	}

	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	all := fs.Bool("all", false, "Scan all targets from curated seed catalog")
	seedsPath := fs.String("seeds", "", "Path to custom seeds JSON file (defaults to embedded seeds)")
	limit := fs.Int("limit", 0, "Limit number of seed targets to process (0 = all)")
	concurrency := fs.Int("concurrency", defaultConcurrency, "Number of concurrent worker goroutines")
	target := fs.String("target", "", "Target ATS URL, board token, or company domain (e.g. boards.greenhouse.io/stripe)")
	githubOrg := fs.String("github", "", "GitHub organization name to mine contributor leads (e.g. stripe)")
	tokensFlag := fs.String("tokens", "", "Comma-separated GitHub tokens for round-robin rotation (or GITHUB_TOKENS env)")
	roleFilter := fs.String("role", "backend", "Target role focus (default: backend)")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	outDir := fs.String("out-dir", "reports", "Directory to write Markdown reports")
	ttlDays := fs.Int("ttl", defaultTTL, "Frontier deduplication TTL in days (re-crawl if older)")
	timeoutSec := fs.Int("timeout", 15, "HTTP request timeout in seconds")
	forceRefresh := fs.Bool("force", false, "Force refresh and bypass crawler frontier deduplication cache")
	syncSheets := fs.Bool("sync-sheets", false, "Automatically sync newly discovered leads & jobs to Google Sheets upon completion")
	mineLeads := fs.Bool("mine-leads", os.Getenv("SPECTER_MINE_LEADS") == "1", "Opt in to mining public GitHub commit metadata for contributor leads/emails (off by default; see SECURITY.md)")
	notifyFlag := fs.Bool("notify", false, "Send new-role alerts via configured webhook/Slack/Telegram (see .env.example)")
	webhookURL := fs.String("webhook", "", "Custom Google Sheets webhook URL override")

	fs.Parse(args)

	// Passing an explicit --github org is an explicit request to mine it.
	if *githubOrg != "" {
		*mineLeads = true
	}

	// If neither --all nor --target was explicitly passed, default to all seeds
	if !*all && *target == "" {
		*all = true
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
	if *forceRefresh || *target != "" {
		crawlTTL = 0
	}

	miner := signals.NewGitMiner(fetcher, signals.GitMinerOptions{
		GitHubTokens:   ghTokens,
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

	fetcher.OnRequestDone = func(status int) {
		tracker.IncRequests(1)
	}

	fmt.Printf("\n⚡ \033[1;36mSPECTER AUTONOMOUS RECON ENGINE v%s\033[0m\n", version)
	fmt.Printf("   Run ID:      \033[1;32m%s\033[0m\n", runID)
	fmt.Printf("   Run Dir:     \033[1m%s\033[0m\n", runDir)
	fmt.Printf("   Targets:     \033[1m%d companies queued\033[0m\n", len(targetSeeds))
	fmt.Printf("   Concurrency: \033[1m%d workers\033[0m\n", *concurrency)
	fmt.Printf("   Lead mining: \033[1m%v\033[0m\n", *mineLeads)
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

	sc := &scanner.Scanner{
		Ingester:  registry,
		Miner:     miner,
		Frontier:  frontier,
		Store:     store,
		Observer:  tracker,
		MineLeads: *mineLeads,
		TTL:       crawlTTL,
		Report: func(ctx context.Context, domain string) (string, error) {
			reportCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return reporter.GenerateMarkdownReport(reportCtx, store, domain, runDir)
		},
	}

	for _, s := range targetSeeds {
		seed := s
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			res, err := sc.ProcessSeed(ctx, seed)
			if err != nil {
				tracker.Log("⚠️  %s: %v", seed.Name, err)
				return nil
			}
			totalNewRoles.Add(int64(res.NewRoles))
			totalNewLeads.Add(int64(res.NewLeads))
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

	if *notifyFlag && len(freshRoles) > 0 {
		notifiers := notify.FromEnv()
		if len(notifiers) == 0 {
			fmt.Fprintln(os.Stderr, "⚠️  --notify set but no SPECTER_WEBHOOK_URL / SPECTER_SLACK_WEBHOOK / SPECTER_TELEGRAM_* configured")
		} else {
			msg := notify.MessageFromRoles(freshRoles, signals.GetUserSkills(), 15)
			nctx, ncancel := context.WithTimeout(context.Background(), 30*time.Second)
			if nErr := notify.SendAll(nctx, notifiers, msg); nErr != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Notification error: %v\n", nErr)
			} else {
				fmt.Printf("🔔 Sent alert for %d new role(s) to %d channel(s)\n", len(freshRoles), len(notifiers))
			}
			ncancel()
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

	// Dispatch Google Sheets synchronization if requested or prompted interactively
	shouldSync := *syncSheets
	if !shouldSync && !nonInteractive && isInteractiveTerminal() {
		unsyncedLeads, _ := store.CountUnsyncedEngineeringLeads(context.Background())
		unsyncedJobs := len(freshRoles)
		if unsyncedLeads > 0 || unsyncedJobs > 0 {
			fmt.Printf("\n📤 Sync discovered contacts (%d) & jobs (%d) to Google Sheets now? [Y/n]: ", unsyncedLeads, unsyncedJobs)
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err == nil {
				trimmed := strings.TrimSpace(strings.ToLower(input))
				if trimmed == "" || trimmed == "y" || trimmed == "yes" {
					shouldSync = true
				}
			}
		}
	}

	if shouldSync {
		fmt.Println()
		sheetsClient := sync.NewSheetsClient(*webhookURL)
		_, _ = sheetsClient.SyncAll(context.Background(), store, sync.SyncOptions{
			Limit: 250,
			LogFunc: func(format string, a ...any) {
				fmt.Printf(format, a...)
			},
		})
	}

	printCatalogSummary(store)
}

func detectATSTypeAndBoard(target string) (string, string) {
	lower := strings.ToLower(target)
	parts := strings.Split(strings.Trim(target, "/"), "/")
	last := target
	if len(parts) > 1 {
		last = parts[len(parts)-1]
	}

	switch {
	case strings.Contains(lower, "greenhouse.io"):
		return "greenhouse", last
	case strings.Contains(lower, "lever.co"):
		return "lever", last
	case strings.Contains(lower, "ashbyhq.com"):
		return "ashby", last
	case strings.Contains(lower, "workable.com"):
		// apply.workable.com/<account>[/...]: the account is the first path segment.
		if len(parts) > 1 {
			for i, p := range parts {
				if strings.Contains(strings.ToLower(p), "workable.com") && i+1 < len(parts) {
					return "workable", parts[i+1]
				}
			}
		}
		return "workable", target
	case strings.Contains(lower, "smartrecruiters.com"):
		for i, p := range parts {
			if strings.Contains(strings.ToLower(p), "smartrecruiters.com") && i+1 < len(parts) {
				return "smartrecruiters", parts[i+1]
			}
		}
		return "smartrecruiters", target
	case strings.Contains(lower, "recruitee.com"):
		host := strings.TrimPrefix(strings.TrimPrefix(lower, "https://"), "http://")
		if i := strings.Index(host, ".recruitee.com"); i > 0 {
			return "recruitee", host[:i]
		}
		return "recruitee", target
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

// runServe starts the local web UI (loopback only by default).
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "Listen address (loopback only unless --allow-remote)")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	openBrowser := fs.Bool("open", false, "Open the UI in your default browser")
	allowRemote := fs.Bool("allow-remote", false, "Allow binding to a non-loopback address (the UI has no authentication)")
	fs.Parse(args)

	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid --addr %q: %v\n", *addr, err)
		os.Exit(1)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) && !*allowRemote {
		fmt.Fprintf(os.Stderr, "Refusing to bind %s: the UI has no authentication. Use 127.0.0.1 or pass --allow-remote.\n", *addr)
		os.Exit(1)
	}

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot listen on %s: %v\n", *addr, err)
		os.Exit(1)
	}
	url := "http://" + ln.Addr().String()
	fmt.Printf("⚡ Specter UI: %s  (Ctrl+C to stop)\n", url)
	if *openBrowser {
		openURL(url)
	}

	webSrv := web.New(store, web.Options{
		Skills: signals.GetUserSkills(),
		Run: func(ctx context.Context, opts runner.Options, prog *web.Progress) (runner.Result, error) {
			return runner.Run(ctx, store, opts, prog)
		},
	})
	defer webSrv.Close()
	srv := &http.Server{Handler: webSrv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// runWatch repeats a scan on an interval, for unattended use (systemd, Docker, tmux).
// Usage: specter watch [--every=6h] [scan flags...]; --notify is enabled by default.
func runWatch(args []string) {
	every := 6 * time.Hour
	var scanArgs []string
	hasNotify := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--every=") || strings.HasPrefix(a, "-every="):
			d, err := time.ParseDuration(a[strings.Index(a, "=")+1:])
			if err != nil || d < time.Minute {
				fmt.Fprintf(os.Stderr, "Invalid --every %q (use a duration >= 1m, e.g. 6h)\n", a)
				os.Exit(1)
			}
			every = d
		default:
			if strings.Contains(a, "notify") {
				hasNotify = true
			}
			scanArgs = append(scanArgs, a)
		}
	}
	if !hasNotify {
		scanArgs = append(scanArgs, "--notify")
	}
	nonInteractive = true

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	for {
		fmt.Printf("\n⏱️  [%s] watch: starting scan (next in %s)\n", time.Now().Format(time.RFC3339), every)
		runScan(scanArgs)
		select {
		case <-ctx.Done():
			fmt.Println("watch: stopped")
			return
		case <-time.After(every):
		}
	}
}

// runFeeds ingests multi-company job aggregators (RemoteOK, Hacker News "Who is hiring?").
func runFeeds(args []string) {
	fs := flag.NewFlagSet("feeds", flag.ExitOnError)
	source := fs.String("source", strings.Join(feeds.Names(), ","), "Comma-separated feeds to ingest ("+strings.Join(feeds.Names(), ", ")+")")
	dbPath := fs.String("db", "specter.db", "SQLite database path")
	timeoutSec := fs.Int("timeout", 20, "HTTP request timeout in seconds")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed opening database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	fetcher := crawler.NewFetcher(crawler.FetcherConfig{
		Timeout:      time.Duration(*timeoutSec) * time.Second,
		MaxBodyBytes: 20 * 1024 * 1024,
	})
	available := feeds.All(fetcher)

	failed := false
	for _, name := range strings.Split(*source, ",") {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		feed, ok := available[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Unknown feed %q (available: %s)\n", name, strings.Join(feeds.Names(), ", "))
			failed = true
			continue
		}

		metas, err := feed.Fetch(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  %s: %v\n", name, err)
			failed = true
			continue
		}

		totalRoles, newRoles := 0, 0
		for _, meta := range metas {
			_, n, saveErr := store.SaveCompanyAndRoles(ctx, meta)
			if saveErr != nil {
				fmt.Fprintf(os.Stderr, "⚠️  %s: saving %s: %v\n", name, meta.Name, saveErr)
				continue
			}
			totalRoles += len(meta.OpenRoles)
			newRoles += n
		}
		fmt.Printf("✅ %s: %d companies, %d matching roles (%d new)\n", name, len(metas), totalRoles, newRoles)
	}
	if failed {
		os.Exit(1)
	}
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

		userSkills := signals.GetUserSkills()
		for i := range engLeads {
			if engLeads[i].PersonalScore == 0 {
				pScore, pMatches := signals.CalculateSkillMatch(engLeads[i].Role+" "+engLeads[i].TopLanguages+" "+strings.Join(engLeads[i].MatchedSignals, " "), userSkills)
				engLeads[i].PersonalScore = pScore
				engLeads[i].PersonalMatches = pMatches
			}
		}

		fmt.Printf("\n📋 \033[1mDISCOVERED ENGINEERING LEADS (%d found)\033[0m\n", len(engLeads))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tSCORE\tTECH MATCH\tNAME\tARCHETYPE\tEMAIL\tGITHUB\tPROVENANCE\tCONTACTED")
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
			matchDisplay := fmt.Sprintf("🎯 %d%%", l.PersonalScore)
			if len(l.PersonalMatches) > 0 {
				matchDisplay = fmt.Sprintf("🎯 %d%% [%s]", l.PersonalScore, strings.Join(l.PersonalMatches, ", "))
			}
			fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				l.ID, l.RelevanceScore, matchDisplay, l.Name, roleDisplay, l.Email, gh, provDisplay, contactStatus)
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

	userSkills := signals.GetUserSkills()
	for i := range roles {
		corpus := roles[i].Title + " " + roles[i].Department + " " + roles[i].Location + " " + roles[i].RemotePolicy + " " + strings.Join(roles[i].Keywords, " ")
		score, matches := signals.CalculateSkillMatch(corpus, userSkills)
		roles[i].PersonalScore = score
		roles[i].PersonalMatches = matches
	}

	sort.SliceStable(roles, func(i, j int) bool {
		if roles[i].PersonalScore != roles[j].PersonalScore {
			return roles[i].PersonalScore > roles[j].PersonalScore
		}
		if roles[i].IsNew != roles[j].IsNew {
			return roles[i].IsNew
		}
		return false
	})

	fmt.Println()
	fmt.Printf("🎯 \033[1;36mSPECTER DIRECT ONE-CLICK APPLY BOARD\033[0m (%d roles found | Personal Tech Match: \033[1;33m%s\033[0m)\n", len(roles), strings.Join(userSkills, ", "))
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════════════════════════════════════════════")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TECH MATCH\tSTATUS\tCOMPANY\tROLE TITLE\tPOLICY\tDIRECT APPLY LINK")
	for i, r := range roles {
		if i >= 40 {
			fmt.Fprintf(w, "... and %d more roles in database. Use --export to generate full markdown.\n", len(roles)-40)
			break
		}
		status := "Active"
		if r.IsNew {
			status = "🔥 NEW"
		}
		matchBadge := fmt.Sprintf("🎯 %d%%", r.PersonalScore)
		if len(r.PersonalMatches) > 0 {
			matchBadge = fmt.Sprintf("🎯 %d%% [%s]", r.PersonalScore, strings.Join(r.PersonalMatches, ", "))
		}
		comp := r.CompanyDomain
		if comp == "" {
			comp = extractDomainName(r.URL)
		}
		applyURL := r.ApplyURL
		if applyURL == "" {
			applyURL = r.URL
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", matchBadge, status, comp, r.Title, r.RemotePolicy, applyURL)
	}
	w.Flush()
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════════════════════════════════════════════")

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
	fmt.Println(`⚡ SPECTER - Fast Recon & Global Remote Job Discovery Engine

🚀 Quick Start (Zero Flags Needed):
  ./specter run                     Run full scan & sync everything to Google Sheets
  ./specter run --limit=5           Quick run on first 5 companies
  ./specter scan                    Scan all companies for open jobs & technical leads
  ./specter sync                    Sync mined leads and jobs to Google Sheets
  ./specter apply --global-only     View one-click remote jobs (worldwide / contractor)
  ./specter leads list              View discovered technical leads

🛠️ Standard Commands:
  ./specter serve [--addr=127.0.0.1:8080] [--open]   Local web UI to search & filter jobs
  ./specter watch [--every=6h]             Re-scan on an interval and alert on new roles
  ./specter feeds [--source=remoteok,hn]   Ingest RemoteOK & Hacker News "Who is hiring?"
  ./specter scan [--target=<url>] [--github=<org>] [--force] [--sync-sheets]
  ./specter sync airtable|notion [--global-only] [--fresh-only] [--limit=200]
  ./specter sync sheets [--target=all|leads|jobs] [--global-only] [--all] [--dry-run]
  ./specter apply [--global-only] [--fresh-only] [--domain=<domain>] [--export]
  ./specter leads list [--domain=<domain>] [--uncontacted]
  ./specter export [--format=json|csv|roles-csv] [--output=<file>]`)
}

func isInteractiveTerminal() bool {
	fileInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
