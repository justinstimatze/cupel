package main

// db_sync.go — `cupel db-sync`: read the existing MD-based corpus and
// populate the SQLite index. This is the one-shot import side of the Phase A
// substrate migration. The MD files remain on disk unchanged for now; the
// renderer rewire (Phase A part 2) will flip the source-of-truth and the MD
// front-matter will be stripped at that point.

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runDBSync(args []string) {
	fs := flag.NewFlagSet("db-sync", flag.ExitOnError)
	dbPath := fs.String("db", "cupel.db", "SQLite database path")
	worksDir := fs.String("works", "works", "works/ directory")
	theoryDir := fs.String("theory", "theory", "theory/ directory")
	_ = fs.Parse(args)

	// Fresh DB every run — the import is idempotent + the DB is derived.
	// Keeps schema migrations free during Phase A.
	_ = os.Remove(*dbPath)
	db, err := openDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	worksImported, refsImported, refsSkipped := importWorks(db, *worksDir)
	theoryImported := importTheoryDocs(db, *theoryDir)

	fmt.Printf("db-sync: imported %d works, %d related_refs (%d skipped — unresolved targets), %d theory docs\n",
		worksImported, refsImported, refsSkipped, theoryImported)
}

func importWorks(db *sql.DB, worksDir string) (works, refs, refsSkipped int) {
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: begin tx: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = tx.Rollback() }()

	insertWork, err := tx.Prepare(`INSERT INTO works (slug, work, author, year, medium, backing, source, layer, verified, author_note, translator, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: prepare works: %v\n", err)
		os.Exit(1)
	}
	defer insertWork.Close()

	insertEngine, err := tx.Prepare(`INSERT INTO work_engines (work_slug, engine_name, ordinal) VALUES (?, ?, ?)`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: prepare work_engines: %v\n", err)
		os.Exit(1)
	}
	defer insertEngine.Close()

	matches, _ := filepath.Glob(filepath.Join(worksDir, "*.md"))
	sort.Strings(matches)

	// First pass: insert every work + engines. We need this complete before
	// the related_refs second pass because related_refs.to_slug is validated
	// against the works table for kind=work.
	type pendingRef struct {
		from  string
		kind  string
		to    string
		gloss string
		ord   int
	}
	var pending []pendingRef

	for _, path := range matches {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_") {
			continue
		}
		slug := strings.TrimSuffix(base, ".md")
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "db-sync: read %s: %v\n", path, err)
			continue
		}
		text := strings.ReplaceAll(string(data), "\r", "")
		fm, body, ferr := splitFrontMatter(text)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "db-sync: parse %s: %v\n", path, ferr)
			continue
		}
		if fm.Work == "" {
			continue
		}
		verifiedVal := sql.NullInt64{}
		if fm.Verified != nil {
			verifiedVal.Valid = true
			if *fm.Verified {
				verifiedVal.Int64 = 1
			}
		}
		_, err = insertWork.Exec(
			slug, fm.Work, fm.Author, fm.Year, fm.Medium, fm.Backing,
			nullable(fm.Source), nullable(fm.Layer), verifiedVal, nullable(fm.AuthorNote),
			nullable(fm.Translator),
			body,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "db-sync: insert work %s: %v\n", slug, err)
			continue
		}
		works++

		// Engines: yaml.v3 parses `engines: [a, b, c]` into a typed []string.
		for i, name := range fm.Engines {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, err := insertEngine.Exec(slug, name, i); err != nil {
				fmt.Fprintf(os.Stderr, "db-sync: insert engine %s/%s: %v\n", slug, name, err)
			}
		}

		// Queue related_refs for the second pass. parseRelatedLine splits the
		// "slug :: gloss" bullet form into a typed relatedRef.
		for i, line := range fm.RelatedWorks {
			if r, ok := parseRelatedLine(line); ok {
				pending = append(pending, pendingRef{slug, "work", r.Slug, r.Gloss, i})
			}
		}
		for i, line := range fm.RelatedTheory {
			if r, ok := parseRelatedLine(line); ok {
				pending = append(pending, pendingRef{slug, "theory", r.Slug, r.Gloss, i + 1000})
			}
		}
		for i, line := range fm.PendingRefs {
			if r, ok := parseRelatedLine(line); ok {
				pending = append(pending, pendingRef{slug, "pending", r.Slug, r.Gloss, i + 2000})
			}
		}
	}

	insertRef, err := tx.Prepare(`INSERT INTO related_refs (from_slug, kind, to_slug, gloss, ordinal) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: prepare related_refs: %v\n", err)
		os.Exit(1)
	}
	defer insertRef.Close()

	// Second pass: validate-and-insert. For kind=work, the target must exist
	// in works. For kind=theory, deferred to importTheoryDocs's validation
	// — Phase A part 2 will tighten this with a foreign key. For kind=pending,
	// freeform.
	worksSet := map[string]bool{}
	rows, _ := tx.Query(`SELECT slug FROM works`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		worksSet[s] = true
	}
	rows.Close()

	for _, r := range pending {
		if r.kind == "work" && !worksSet[r.to] {
			refsSkipped++
			continue
		}
		if _, err := insertRef.Exec(r.from, r.kind, r.to, r.gloss, r.ord); err != nil {
			fmt.Fprintf(os.Stderr, "db-sync: insert ref %s→%s/%s: %v\n", r.from, r.kind, r.to, err)
			continue
		}
		refs++
	}

	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: commit: %v\n", err)
		os.Exit(1)
	}
	return
}

func importTheoryDocs(db *sql.DB, theoryDir string) int {
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: theory begin: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = tx.Rollback() }()

	insertDoc, err := tx.Prepare(`INSERT INTO theory_docs (slug, title, body) VALUES (?, ?, ?)`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: prepare theory_docs: %v\n", err)
		os.Exit(1)
	}
	defer insertDoc.Close()

	docs := loadTheoryDocs(theoryDir)
	for _, d := range docs {
		if _, err := insertDoc.Exec(d.Slug, d.Title, d.Body); err != nil {
			fmt.Fprintf(os.Stderr, "db-sync: insert theory %s: %v\n", d.Slug, err)
			continue
		}
	}
	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "db-sync: theory commit: %v\n", err)
		os.Exit(1)
	}
	return len(docs)
}

func nullable(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
