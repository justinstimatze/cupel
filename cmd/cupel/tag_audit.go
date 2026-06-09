package main

// tag_audit.go — keeps the engine-tag namespace from drifting. Walks
// works/*.md, extracts every engine-tag line, and verifies
// the tag name is either: (a) one of the confirmed engines listed in
// engines.json, (b) a base-engine + "-antagonist-mode" variant, or (c) on the
// small allow-list of non-engine tag-shapes the catalog admits (solvents and
// refusal-modes). Anything else is drift and the audit fails non-zero.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Non-engine tags the catalog admits in the engine-list of a dossier — kept
// small on purpose; expand here only when the catalog deliberately graduates a
// new category.
var allowedNonEngineTags = map[string]bool{
	"vindication-solvent":                  true,
	"cluster-internal-participant-refusal": true,
	"model-refusal":                        true,
}

// engineTagLine matches the dossier convention: "- **name** · layer · role · tier"
var engineTagAuditLine = regexp.MustCompile(`(?m)^-\s+\*\*([^*]+?)\*\*\s+·\s+[^·]+?\s+·\s+[^·]+?\s+·\s+(✓|~)`)

func runTagAudit(args []string) {
	fs := flag.NewFlagSet("tag-audit", flag.ContinueOnError)
	dirs := fs.String("dirs", "works", "comma-separated dirs to scan")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	sub, err := loadSubstrate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tag-audit: load substrate:", err)
		os.Exit(2)
	}
	confirmed := map[string]bool{}
	for _, e := range sub.Engines {
		confirmed[e.Name] = true
	}

	type hit struct {
		file string
		line int
		tag  string
	}
	var drift []hit
	scanned := 0
	for _, d := range strings.Split(*dirs, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			scanned++
			text := string(b)
			for _, mm := range engineTagAuditLine.FindAllStringSubmatchIndex(text, -1) {
				name := text[mm[2]:mm[3]]
				if isCanonicalTag(name, confirmed) {
					continue
				}
				line := 1 + strings.Count(text[:mm[0]], "\n")
				drift = append(drift, hit{file: m, line: line, tag: name})
			}
		}
	}

	if len(drift) == 0 {
		fmt.Fprintf(os.Stderr, "tag-audit: clean across %d file(s) — %d confirmed engines + %d allowed non-engine tags\n",
			scanned, len(confirmed), len(allowedNonEngineTags))
		return
	}

	sort.Slice(drift, func(i, j int) bool {
		if drift[i].file != drift[j].file {
			return drift[i].file < drift[j].file
		}
		return drift[i].line < drift[j].line
	})
	fmt.Fprintf(os.Stderr, "tag-audit: %d drift hit(s) — non-canonical engine tags:\n", len(drift))
	for _, h := range drift {
		fmt.Fprintf(os.Stderr, "  %s:%d  **%s**\n", h.file, h.line, h.tag)
	}
	fmt.Fprintf(os.Stderr, "\nFix: rename to a confirmed engine, an `<engine>-antagonist-mode` variant, or graduate via the allow-list in cmd/cupel/tag_audit.go.\n")
	os.Exit(1)
}

// isCanonicalTag returns true iff name is (a) a confirmed engine, (b) a
// "<confirmed-engine>-antagonist-mode" variant, or (c) on the non-engine
// allow-list. Engine names containing "/" (e.g. "order/legibility") have the
// "-antagonist-mode" appended after the last segment ("order/legibility-antagonist-mode").
func isCanonicalTag(name string, confirmed map[string]bool) bool {
	if confirmed[name] {
		return true
	}
	if allowedNonEngineTags[name] {
		return true
	}
	if base, ok := strings.CutSuffix(name, "-antagonist-mode"); ok && confirmed[base] {
		return true
	}
	return false
}
