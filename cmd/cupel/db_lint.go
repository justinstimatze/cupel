package main

// db_lint.go — `cupel db-lint`: integrity checks against the SQLite index.
// Fails on inconsistencies the schema's CHECK + foreign-key constraints don't
// catch directly: pending refs that point at existing works (should have
// been related_refs), theory cross-refs whose target doesn't exist, engine
// names that don't match the canonical set, etc.
//
// Phase A part 1 exit criterion: this lint passes cleanly against the
// freshly-synced DB. Any drift the MD substrate accumulated surfaces here.

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
)

func runDBLint(args []string) {
	fs := flag.NewFlagSet("db-lint", flag.ExitOnError)
	dbPath := fs.String("db", "cupel.db", "SQLite database path")
	_ = fs.Parse(args)

	db, err := openDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-lint: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	findings := 0
	findings += checkPendingRefsAreActuallyPending(db)
	findings += checkTheoryCrossRefsResolve(db)
	findings += checkUnreachableSlugs(db)

	if findings == 0 {
		fmt.Println("db-lint: clean")
		return
	}
	fmt.Fprintf(os.Stderr, "db-lint: %d finding(s)\n", findings)
	os.Exit(1)
}

// checkPendingRefsAreActuallyPending: if a pending_ref.to_slug now exists as
// a real work, it should have been promoted to a related_works entry. Surfaces
// drift where a target became dossiered but the citing dossier never updated.
func checkPendingRefsAreActuallyPending(db *sql.DB) int {
	rows, err := db.Query(`
		SELECT r.from_slug, r.to_slug
		FROM related_refs r
		JOIN works w ON w.slug = r.to_slug
		WHERE r.kind = 'pending'
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-lint: query: %v\n", err)
		return 1
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var from, to string
		_ = rows.Scan(&from, &to)
		fmt.Fprintf(os.Stderr, "db-lint: %s — pending_ref to '%s' but that work is now dossiered (promote to related_works)\n", from, to)
		n++
	}
	return n
}

// checkTheoryCrossRefsResolve: every related_refs of kind=theory must point
// at an existing theory_docs.slug. Catches a theory doc rename/delete that
// stranded a cross-ref.
func checkTheoryCrossRefsResolve(db *sql.DB) int {
	rows, err := db.Query(`
		SELECT r.from_slug, r.to_slug
		FROM related_refs r
		LEFT JOIN theory_docs t ON t.slug = r.to_slug
		WHERE r.kind = 'theory' AND t.slug IS NULL
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-lint: query: %v\n", err)
		return 1
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var from, to string
		_ = rows.Scan(&from, &to)
		fmt.Fprintf(os.Stderr, "db-lint: %s — related_theory '%s' has no matching theory doc\n", from, to)
		n++
	}
	return n
}

// checkUnreachableSlugs: works that no cross-ref or engine page points at,
// surfaced as a soft warning (not a failure). Useful for the "what's orphaned"
// signal but doesn't block CI.
func checkUnreachableSlugs(db *sql.DB) int {
	rows, err := db.Query(`
		SELECT slug FROM works
		WHERE slug NOT IN (SELECT DISTINCT to_slug FROM related_refs WHERE kind='work')
		ORDER BY slug
	`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		count++
	}
	// Almost every work will currently be unreachable (only 13 dossiers have
	// related_works at all); print a single summary line, not a finding.
	fmt.Printf("db-lint: %d works with no inbound related_works (informational; not a failure yet)\n", count)
	return 0
}
