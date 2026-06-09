package main

// db_load.go — read-side of the SQLite substrate. loadWorksFromDB returns the
// same []workCard slice loadWorks returns, but sourced from the DB-validated
// structural data instead of raw MD front-matter. Body parsing (engine
// bullets, bead extraction, reading/evidence split) still runs on the body
// string — those are markdown-structural, not metadata-structural, and the
// body is preserved verbatim through sync.

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
)

// loadWorksFromDB returns workCards built from the SQLite index. Equivalent
// in shape to the MD-based loadWorks but with structural data sourced from the
// validated DB rows. The body remains markdown — body-parsing helpers run on
// it as they did before.
func loadWorksFromDB(dbPath string) ([]workCard, error) {
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT slug, work, author, year, medium, backing, source, layer, author_note, translator, body FROM works`)
	if err != nil {
		return nil, fmt.Errorf("query works: %w", err)
	}
	defer rows.Close()

	var cards []workCard
	for rows.Next() {
		var (
			slug, work, author, year, medium, backing, body string
			source, layer, authorNote, translator           sql.NullString
		)
		if err := rows.Scan(&slug, &work, &author, &year, &medium, &backing, &source, &layer, &authorNote, &translator, &body); err != nil {
			return nil, fmt.Errorf("scan work row: %w", err)
		}
		c := workCard{
			Slug: slug, File: slug + ".md",
			Work: work, Author: author, Year: year, Medium: medium, Backing: backing,
			Source: source.String, AuthorNote: authorNote.String, Translator: translator.String,
		}
		// body-derived fields use the same helpers as the MD path
		body = strings.ReplaceAll(body, "\r", "")
		reading, evidencePresent := splitWorkBody(body)
		c.Bead = sectionText(body, "**The bead.**")
		c.ReadingBody = mdBlock(reading)
		c.FullBody = mdBlock(body)
		c.HasEvidence = evidencePresent
		for _, mm := range engineLine.FindAllStringSubmatch(body, -1) {
			role := strings.Trim(mm[3], "*")
			c.Engines = append(c.Engines, engineTag{
				Name: mm[1], Role: role, Tier: mm[4],
				Excluded: strings.Contains(strings.ToLower(role), "exclud"),
			})
		}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate works: %w", err)
	}

	// related_refs: attach to the in-memory cards by from_slug, ordered by ordinal.
	bySlug := map[string]*workCard{}
	for i := range cards {
		bySlug[cards[i].Slug] = &cards[i]
	}
	refRows, err := db.Query(`SELECT from_slug, kind, to_slug, gloss FROM related_refs ORDER BY from_slug, ordinal`)
	if err != nil {
		return nil, fmt.Errorf("query related_refs: %w", err)
	}
	defer refRows.Close()
	for refRows.Next() {
		var from, kind, to, gloss string
		if err := refRows.Scan(&from, &kind, &to, &gloss); err != nil {
			return nil, fmt.Errorf("scan related_refs: %w", err)
		}
		card, ok := bySlug[from]
		if !ok {
			continue
		}
		r := relatedRef{Slug: to, Gloss: gloss}
		switch kind {
		case "work":
			card.RelatedWorks = append(card.RelatedWorks, r)
		case "theory":
			card.RelatedTheory = append(card.RelatedTheory, r)
		case "pending":
			card.PendingRefs = append(card.PendingRefs, r)
		}
	}

	sort.Slice(cards, func(i, j int) bool { return cards[i].Work < cards[j].Work })
	return cards, nil
}

// requireDB returns the DB path that should be used; fails fast if the DB
// doesn't exist (build-data depends on db-sync having run first).
func requireDB(path string) string {
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "cupel: %s not found — run `cupel db-sync` first\n", path)
		os.Exit(1)
	}
	return path
}
