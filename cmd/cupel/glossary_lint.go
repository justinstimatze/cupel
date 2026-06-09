package main

// glossary_lint.go — `cupel glossary-lint`: structural validator for
// theory/glossary.md and theory/glossary-linkable.txt. The existing
// loadGlossary tolerates duplicate slugs (silent overwrites) and the
// linkable allow-list is never checked against the actual glossary
// entries, so a stale linkable entry just stays inert. This surfaces
// both at commit time.
//
// Findings (each fires its own line of output; exit 1 on any failure):
//   - a duplicate glossary slug across entries
//   - a theory/glossary-linkable.txt line that doesn't match any entry slug
//
// Alias-slug overlap with another entry's canonical slug is NOT flagged —
// paired forms (`engine ↔ texture`, `enabling ↔ counterfeit`) intentionally
// produce aliases that point at standalone primary entries; the linker
// routes both to the primary by design.

import (
	"flag"
	"fmt"
	"os"
)

func runGlossaryLint(args []string) {
	fs := flag.NewFlagSet("glossary-lint", flag.ContinueOnError)
	path := fs.String("glossary", "theory/glossary.md", "glossary markdown path")
	linkablePath := fs.String("linkable", "theory/glossary-linkable.txt", "linkable allow-list path")
	_ = fs.Parse(args)

	linkable := loadLinkableSlugs(*linkablePath)
	entries, err := loadGlossaryWithLinkable(*path, linkable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "glossary-lint: load %s: %v\n", *path, err)
		os.Exit(1)
	}

	var findings []string
	emit := func(s string) { findings = append(findings, s) }

	// 1. Duplicate slugs.
	bySlug := map[string]glossaryEntry{}
	for _, e := range entries {
		if _, ok := bySlug[e.Slug]; ok {
			emit(fmt.Sprintf("duplicate glossary slug %q (term=%q section=%q)", e.Slug, e.Term, e.Section))
			continue
		}
		bySlug[e.Slug] = e
	}

	// 2. Linkable allow-list entries that don't match any glossary slug.
	for slug := range linkable {
		if _, ok := bySlug[slug]; !ok {
			emit(fmt.Sprintf("theory/glossary-linkable.txt entry %q has no matching glossary entry", slug))
		}
	}

	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "glossary-lint: clean across %d entries (%d linkable)\n", len(entries), len(linkable))
		return
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f)
	}
	fmt.Fprintf(os.Stderr, "\nglossary-lint: %d finding(s) across %d entries\n", len(findings), len(entries))
	os.Exit(1)
}
