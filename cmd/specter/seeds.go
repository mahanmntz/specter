package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/seedtools"
	"github.com/mahanmntz/specter/internal/storage"
)

const seedsUsage = `Usage:
  specter seeds list                              Show the company catalog Specter scans
  specter seeds check [--prune] [--repair]        Find dead job boards (404); --prune switches them off,
                                                  --repair also looks for the company's new board
  specter seeds export                            Print the effective catalog as JSON
  specter seeds import remoteintech [--discover]  Add remote-friendly companies from the remoteintech directory
  specter seeds reset                             Forget imported and disabled seeds`

func runSeeds(args []string) {
	if len(args) == 0 {
		fmt.Println(seedsUsage)
		return
	}
	switch args[0] {
	case "list":
		seedsList(args[1:])
	case "check":
		seedsCheck(args[1:])
	case "import":
		seedsImport(args[1:])
	case "export":
		seedsExport(args[1:])
	case "reset":
		seedsReset(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown seeds command %q\n\n%s\n", args[0], seedsUsage)
		os.Exit(1)
	}
}

func openSeedStore(fs *flag.FlagSet) (*storage.Store, context.Context, context.CancelFunc) {
	dbPath := fs.Lookup("db").Value.String()
	store, err := storage.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return store, ctx, cancel
}

func seedFetcher(timeout time.Duration) *crawler.Fetcher {
	return crawler.NewFetcher(crawler.FetcherConfig{Timeout: timeout, MaxBodyBytes: 5 * 1024 * 1024, TokenPool: crawler.NewTokenPoolFromEnv()})
}

func seedsList(args []string) {
	fs := flag.NewFlagSet("seeds list", flag.ExitOnError)
	fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)
	store, ctx, cancel := openSeedStore(fs)
	defer store.Close()
	defer cancel()

	eff, err := seeds.Effective(ctx, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	user, _ := seeds.User(ctx, store)
	disabled, _ := seeds.Disabled(ctx, store)
	byATS := map[string]int{}
	for _, s := range eff {
		byATS[s.ATSService]++
	}
	names := make([]string, 0, len(byATS))
	for n := range byATS {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("%d companies will be scanned (%d imported by you, %d switched off)\n", len(eff), len(user), len(disabled))
	for _, n := range names {
		fmt.Printf("  %-16s %d\n", n, byATS[n])
	}
}

func seedsCheck(args []string) {
	fs := flag.NewFlagSet("seeds check", flag.ExitOnError)
	fs.String("db", "specter.db", "SQLite database path")
	prune := fs.Bool("prune", false, "Switch off seeds whose board no longer exists")
	repair := fs.Bool("repair", false, "Try to find each dead company's current board on Greenhouse/Lever/Ashby (implies --prune)")
	fs.Parse(args)
	store, ctx, cancel := openSeedStore(fs)
	defer store.Close()
	defer cancel()

	eff, err := seeds.Effective(ctx, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Checking %d job boards…\n", len(eff))
	results := seedtools.Check(ctx, seedFetcher(15*time.Second), eff, 6, func(done, total int) {
		fmt.Fprintf(os.Stderr, "\r  %d/%d", done, total)
	})
	fmt.Fprintln(os.Stderr)

	counts := map[seedtools.Status]int{}
	var stale []string
	for _, r := range results {
		counts[r.Status]++
		switch r.Status {
		case seedtools.StatusStale:
			fmt.Printf("✗ %-28s %s/%s  (%s)\n", r.Seed.Name, r.Seed.ATSService, r.Seed.ATSBoardID, r.Detail)
			stale = append(stale, seeds.Key(r.Seed))
		case seedtools.StatusUnreachable:
			fmt.Printf("? %-28s %s/%s  (%s; not conclusive)\n", r.Seed.Name, r.Seed.ATSService, r.Seed.ATSBoardID, r.Detail)
		}
	}
	fmt.Printf("\n%d ok, %d dead, %d unreachable, %d not checked (own websites)\n",
		counts[seedtools.StatusOK], counts[seedtools.StatusStale], counts[seedtools.StatusUnreachable], counts[seedtools.StatusSkipped])
	if len(stale) == 0 {
		return
	}
	if !*prune && !*repair {
		fmt.Println("Run again with --prune to stop scanning the dead boards, or --repair to also look for their new boards.")
		return
	}

	if *repair {
		f := seedFetcher(15 * time.Second)
		var (
			mu    sync.Mutex
			fixed []seeds.SeedTarget
			g     errgroup.Group
		)
		g.SetLimit(6)
		for _, r := range results {
			r := r
			if r.Status != seedtools.StatusStale {
				continue
			}
			g.Go(func() error {
				if ctx.Err() != nil {
					return nil
				}
				if svc, id, ok := seedtools.Discover(ctx, f, r.Seed.Name, r.Seed.Domain); ok {
					ns := r.Seed
					ns.ATSService, ns.ATSBoardID = svc, id
					mu.Lock()
					fixed = append(fixed, ns)
					fmt.Printf("✓ %-28s %s/%s → %s/%s\n", r.Seed.Name, r.Seed.ATSService, r.Seed.ATSBoardID, svc, id)
					mu.Unlock()
				}
				return nil
			})
		}
		_ = g.Wait()
		if _, err := seeds.AddUser(ctx, store, fixed); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Repaired %d of %d dead boards.\n", len(fixed), len(stale))
	}
	if err := seeds.Disable(ctx, store, stale); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Switched off %d dead boards (undo with: specter seeds reset).\n", len(stale))
}

func seedsExport(args []string) {
	fs := flag.NewFlagSet("seeds export", flag.ExitOnError)
	fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)
	store, ctx, cancel := openSeedStore(fs)
	defer store.Close()
	defer cancel()
	eff, err := seeds.Effective(ctx, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(eff)
}

func seedsImport(args []string) {
	if len(args) == 0 || args[0] != "remoteintech" {
		fmt.Fprintf(os.Stderr, "Usage: specter seeds import remoteintech [--discover] [--limit N]\n")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("seeds import", flag.ExitOnError)
	fs.String("db", "specter.db", "SQLite database path")
	discover := fs.Bool("discover", false, "Also probe Greenhouse/Lever/Ashby for companies whose careers page is their own site (slower)")
	limit := fs.Int("limit", 0, "Only look at the first N companies (0 = all)")
	conc := fs.Int("concurrency", 12, "Parallel probes (requests per host are still paced politely)")
	fs.Parse(args[1:])
	store, ctx, cancel := openSeedStore(fs)
	defer store.Close()
	defer cancel()

	f := seedFetcher(20 * time.Second)
	fmt.Fprintln(os.Stderr, "Downloading the remoteintech directory (github.com/remoteintech/remote-jobs, ISC license)…")
	archive := crawler.NewFetcher(crawler.FetcherConfig{Timeout: 90 * time.Second, MaxBodyBytes: 64 * 1024 * 1024})
	companies, err := seedtools.FetchRemoteIntech(ctx, archive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if *limit > 0 && len(companies) > *limit {
		companies = companies[:*limit]
	}

	fmt.Fprintf(os.Stderr, "Validating job boards for %d companies…\n", len(companies))
	sum := seedtools.BuildSeeds(ctx, f, companies, *discover, *conc, func(done, total int) {
		fmt.Fprintf(os.Stderr, "\r  %d/%d", done, total)
	})
	fmt.Fprintln(os.Stderr)

	added, err := seeds.AddUser(ctx, store, sum.Valid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Read %d companies: %d use a supported job board, %d boards are live", sum.Companies, sum.Detected, len(sum.Valid))
	if *discover {
		fmt.Printf(" (%d found by probing)", sum.Discovered)
	}
	fmt.Printf(".\nAdded %d new companies to your catalog (the rest were already there).\n", added)
}

func seedsReset(args []string) {
	fs := flag.NewFlagSet("seeds reset", flag.ExitOnError)
	fs.String("db", "specter.db", "SQLite database path")
	fs.Parse(args)
	store, ctx, cancel := openSeedStore(fs)
	defer store.Close()
	defer cancel()
	if err := seeds.Reset(ctx, store); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Println("Catalog reset to the built-in companies.")
}

// importRemoteIntech downloads the directory, validates boards and stores new ones; used by the web UI.
func importRemoteIntech(ctx context.Context, store *storage.Store, discover bool, progress func(done, total int)) (int, error) {
	archive := crawler.NewFetcher(crawler.FetcherConfig{Timeout: 90 * time.Second, MaxBodyBytes: 64 * 1024 * 1024})
	companies, err := seedtools.FetchRemoteIntech(ctx, archive)
	if err != nil {
		return 0, err
	}
	sum := seedtools.BuildSeeds(ctx, seedFetcher(20*time.Second), companies, discover, 12, progress)
	return seeds.AddUser(ctx, store, sum.Valid)
}
