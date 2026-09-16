package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/database"
	"github.com/shridarpatil/whatomate/internal/orgpurge"
	"github.com/zerodha/logf"
)

// runOrg dispatches the `wacrm org` subcommands.
//
// Purging is deliberately CLI-only (plan 10, S8). There is no HTTP endpoint,
// because an irreversible delete of an entire tenant should not be one
// mis-authorised request away — it should need shell access to the deployment.
func runOrg(args []string) {
	if len(args) < 1 {
		printOrgUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "purge":
		runOrgPurge(args[1:])
	case "help", "-h", "--help":
		printOrgUsage()
	default:
		fmt.Printf("Unknown org command: %s\n\n", args[0])
		printOrgUsage()
		os.Exit(1)
	}
}

func printOrgUsage() {
	fmt.Println(`Usage:
  wacrm org purge <organization-id> [options]

Permanently deletes an organization and every row belonging to it: contacts,
conversations, messages, campaigns, deals, tasks, automations, audit logs and
users who belong to no other organization. This cannot be undone.

Options:
  -config string    Path to config file (default "config.toml")
  -dry-run          Report what would be deleted, delete nothing
  -yes              Skip the confirmation prompt (for scripted use)`)
}

func runOrgPurge(args []string) {
	purgeFlags := flag.NewFlagSet("org purge", flag.ExitOnError)
	configPath := purgeFlags.String("config", "config.toml", "Path to config file")
	dryRun := purgeFlags.Bool("dry-run", false, "Report what would be deleted, delete nothing")
	assumeYes := purgeFlags.Bool("yes", false, "Skip the confirmation prompt")
	_ = purgeFlags.Parse(args)

	// flag stops parsing at the first positional, and "purge <id> -dry-run" is
	// the order the usage line asks for — so the flags after the id have to be
	// parsed in a second pass or they are silently ignored, which for -dry-run
	// means deleting an organization somebody asked to preview.
	rest := purgeFlags.Args()
	if len(rest) > 1 {
		_ = purgeFlags.Parse(rest[1:])
	}

	if len(rest) < 1 {
		fmt.Println("wacrm org purge needs an organization id")
		printOrgUsage()
		os.Exit(1)
	}

	orgID, err := uuid.Parse(rest[0])
	if err != nil {
		fmt.Printf("%q is not a valid organization id\n", rest[0])
		os.Exit(1)
	}

	lo := logf.New(logf.Opts{
		Level:           logf.InfoLevel,
		TimestampFormat: "2006-01-02 15:04:05",
		DefaultFields:   []any{"app", "wacrm-org"},
	})

	cfg, err := config.Load(*configPath)
	if err != nil {
		lo.Fatal("Failed to load config", "error", err)
	}

	db, err := database.NewPostgres(&cfg.Database, false)
	if err != nil {
		lo.Fatal("Failed to connect to database", "error", err)
	}

	var name string
	if err := db.Raw(`SELECT name FROM organizations WHERE id = ?`, orgID).Scan(&name).Error; err != nil {
		lo.Fatal("Failed to look up organization", "error", err)
	}
	if name == "" {
		fmt.Printf("No organization with id %s\n", orgID)
		os.Exit(1)
	}

	// Showing the counts before the prompt is the whole point of the
	// confirmation: "delete 41,233 messages" is a decision, "are you sure" is
	// not.
	counts, err := orgpurge.CountRemaining(db, orgID)
	if err != nil {
		lo.Fatal("Failed to count organization data", "error", err)
	}

	fmt.Printf("\nOrganization: %s (%s)\n\n", name, orgID)
	if len(counts) == 0 {
		fmt.Println("  no rows outside the organization record itself")
	} else {
		printCounts(counts)
	}

	if *dryRun {
		fmt.Println("\nDry run: nothing was deleted.")
		return
	}

	if !*assumeYes && !confirmPurge(name) {
		fmt.Println("Aborted.")
		return
	}

	result, err := orgpurge.Purge(db, orgID)
	if err != nil {
		lo.Fatal("Purge failed; nothing was deleted", "error", err)
	}

	fmt.Printf("\nDeleted %d rows across %d tables.\n", result.Total, len(result.ByTable))

	leftover, err := orgpurge.CountRemaining(db, orgID)
	if err != nil {
		lo.Error("Purge finished but the verification query failed", "error", err)
		return
	}
	if len(leftover) > 0 {
		fmt.Println("\nWARNING — rows still reference this organization:")
		printCounts(leftover)
		os.Exit(1)
	}
	fmt.Println("Nothing references this organization any more.")
}

func printCounts(counts map[string]int64) {
	tables := make([]string, 0, len(counts))
	for table := range counts {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		fmt.Printf("  %-32s %8d\n", table, counts[table])
	}
}

// confirmPurge makes the operator type the organization's name.
//
// A y/n prompt on an irreversible whole-tenant delete is a reflex, not a
// decision; typing the name means having read which organization this is.
func confirmPurge(name string) bool {
	fmt.Printf("\nThis cannot be undone. Type the organization name to confirm: ")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.TrimSpace(answer) == name
}
