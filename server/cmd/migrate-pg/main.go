// Copies every table from a Proxyness SQLite DB into a Postgres DB with
// identical schema (see server/internal/db/pg/schema.sql). Preserves row IDs
// and advances identity sequences afterwards so newly-inserted rows don't collide.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
)

func main() {
	sqlitePath := flag.String("sqlite", "", "path to source SQLite file (required)")
	pgURL := flag.String("pg", "", "destination Postgres connection URL (required)")
	skipLogs := flag.Bool("skip-logs", false, "skip the logs table (large, optional)")
	truncate := flag.Bool("truncate", false, "TRUNCATE target tables before copy (destructive)")
	flag.Parse()

	if *sqlitePath == "" || *pgURL == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx := context.Background()

	src, err := sql.Open("sqlite", *sqlitePath)
	must(err, "open sqlite")
	defer src.Close()

	dst, err := pgx.Connect(ctx, *pgURL)
	must(err, "connect postgres")
	defer dst.Close(ctx)

	tx, err := dst.Begin(ctx)
	must(err, "begin tx")
	defer tx.Rollback(ctx) //nolint:errcheck // committed below on success

	if *truncate {
		if _, err := tx.Exec(ctx, "TRUNCATE user_sites, site_ips, site_domains, sites, logs, changelog, traffic_stats, devices, users RESTART IDENTITY CASCADE"); err != nil {
			log.Fatalf("truncate: %v", err)
		}
		fmt.Println("truncated")
	}

	if err := copyUsers(ctx, src, tx); err != nil {
		log.Fatalf("users: %v", err)
	}
	if err := copyDevices(ctx, src, tx); err != nil {
		log.Fatalf("devices: %v", err)
	}
	if err := copyTrafficStats(ctx, src, tx); err != nil {
		log.Fatalf("traffic_stats: %v", err)
	}
	if err := copyChangelog(ctx, src, tx); err != nil {
		log.Fatalf("changelog: %v", err)
	}
	if !*skipLogs {
		if err := copyLogs(ctx, src, tx); err != nil {
			log.Fatalf("logs: %v", err)
		}
	}
	if err := copySites(ctx, src, tx); err != nil {
		log.Fatalf("sites: %v", err)
	}
	if err := copySiteDomains(ctx, src, tx); err != nil {
		log.Fatalf("site_domains: %v", err)
	}
	if err := copySiteIPs(ctx, src, tx); err != nil {
		log.Fatalf("site_ips: %v", err)
	}
	if err := copyUserSites(ctx, src, tx); err != nil {
		log.Fatalf("user_sites: %v", err)
	}
	if err := advanceSequences(ctx, tx); err != nil {
		log.Fatalf("advance sequences: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Fatalf("commit: %v", err)
	}
	fmt.Println("migration complete")
}

func must(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %v", msg, err)
	}
}
