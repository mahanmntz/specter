package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"specter/internal/storage"
	"specter/internal/sync"
)

func runSync(args []string) {
	if len(args) == 0 {
		runSyncSheets([]string{})
		return
	}

	subcommand := args[0]
	switch subcommand {
	case "sheets":
		runSyncSheets(args[1:])
	default:
		// If user passed flags directly like `specter sync --all`
		if len(subcommand) > 0 && subcommand[0] == '-' {
			runSyncSheets(args)
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown sync target %q. Supported targets: 'sheets'\n", subcommand)
		fmt.Fprintln(os.Stderr, "Usage: specter sync sheets [--target=all|leads|jobs] [--webhook=<url>] [--all] [--limit=100] [--dry-run]")
		os.Exit(1)
	}
}

func runSyncSheets(args []string) {
	fs := flag.NewFlagSet("sync sheets", flag.ExitOnError)
	webhookFlag := fs.String("webhook", "", "Google Sheets Webhook URL override")
	targetFlag := fs.String("target", "all", "Sync target: 'all' (Contacts & Jobs), 'leads' (Contacts only), or 'jobs' (Jobs only)")
	allFlag := fs.Bool("all", false, "Sync all records regardless of previous sync status")
	globalOnlyFlag := fs.Bool("global-only", false, "For jobs: only sync global / contractor-friendly remote jobs")
	limitFlag := fs.Int("limit", 100, "Maximum number of records to push per tab")
	dryRunFlag := fs.Bool("dry-run", false, "Print JSON payload without sending HTTP requests")
	dbPath := fs.String("db", "specter.db", "SQLite database path")

	fs.Parse(args)

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	client := sync.NewSheetsClient(*webhookFlag)
	opts := sync.SyncOptions{
		WebhookURL: client.WebhookURL,
		Target:     strings.ToLower(*targetFlag),
		All:        *allFlag,
		Limit:      *limitFlag,
		DryRun:     *dryRunFlag,
		GlobalOnly: *globalOnlyFlag,
		LogFunc: func(format string, a ...any) {
			fmt.Printf(format, a...)
		},
	}

	ctx := context.Background()
	var res *sync.SyncResult

	switch strings.ToLower(*targetFlag) {
	case "leads", "contacts":
		res, err = client.SyncLeads(ctx, store, opts)
	case "jobs", "roles":
		res, err = client.SyncJobs(ctx, store, opts)
	default:
		res, err = client.SyncAll(ctx, store, opts)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ [SYNC] Sync error: %v\n", err)
		os.Exit(1)
	}

	if res.TotalProcessed == 0 {
		fmt.Println("[SYNC] No records waiting to sync. Use --all to re-push.")
	}
}
