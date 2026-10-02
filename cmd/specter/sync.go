package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
		fmt.Fprintln(os.Stderr, "Usage: specter sync sheets [--webhook=<url>] [--all] [--limit=100] [--dry-run]")
		os.Exit(1)
	}
}

func runSyncSheets(args []string) {
	fs := flag.NewFlagSet("sync sheets", flag.ExitOnError)
	webhookFlag := fs.String("webhook", "", "Google Sheets Webhook URL override")
	allFlag := fs.Bool("all", false, "Sync all leads regardless of previous sync status")
	limitFlag := fs.Int("limit", 100, "Maximum number of leads to push in this run")
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
		All:        *allFlag,
		Limit:      *limitFlag,
		DryRun:     *dryRunFlag,
		LogFunc: func(format string, a ...any) {
			fmt.Printf(format, a...)
		},
	}

	ctx := context.Background()
	res, err := client.SyncLeads(ctx, store, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ [SYNC] Sync error: %v\n", err)
		os.Exit(1)
	}

	if res.TotalProcessed == 0 {
		fmt.Println("[SYNC] All engineering leads are already synchronized to Google Sheets. Use --all to re-push.")
	}
}
