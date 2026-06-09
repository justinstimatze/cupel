package main

// clusters_lint.go — `cupel clusters-lint`: structural validator for
// theory/cluster-catalog.md. The existing loadClusters silently swallows
// rows that fail to match clusterRow, intros that don't pair to a table row,
// and duplicate slugs. This file's checks surface those at commit time the
// same way works-lint surfaces dossier drift.
//
// Findings (each fires its own line of output; exit 1 on any failure):
//   - a numbered table row (starts `| N`) that doesn't match the row regex
//   - a duplicate cluster slug
//   - a `### <Name>` heading in `## Cluster intros` not matched by a row
//
// The engines-column is freeform prose, not a structured list — we don't
// try to validate engine names from it. Engine-name discipline lives in
// tag-audit, which scans the structured engine tags inside dossier bodies.

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func runClustersLint(args []string) {
	fs := flag.NewFlagSet("clusters-lint", flag.ContinueOnError)
	catalog := fs.String("catalog", "theory/cluster-catalog.md", "cluster catalog markdown")
	_ = fs.Parse(args)

	raw, err := os.ReadFile(*catalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clusters-lint: read %s: %v\n", *catalog, err)
		os.Exit(1)
	}
	text := strings.ReplaceAll(string(raw), "\r", "")

	var findings []string
	emit := func(s string) { findings = append(findings, s) }

	// 1. Row scan: every `| N ` line must match clusterRow.
	rows := []clusterSpec{}
	slugLine := map[string]int{}
	for i, ln := range strings.Split(text, "\n") {
		if !numberedRow.MatchString(ln) {
			continue
		}
		mm := clusterRow.FindStringSubmatch(ln)
		if mm == nil {
			emit(fmt.Sprintf("line %d: numbered table row doesn't match clusterRow regex (`| N | **Name** | Domain | Status | Engines | Specimens |`):\n  %s", i+1, ln))
			continue
		}
		num := 0
		fmt.Sscanf(mm[1], "%d", &num)
		name := strings.TrimSpace(mm[2])
		slug := clusterSlug(name)
		if prev, ok := slugLine[slug]; ok {
			emit(fmt.Sprintf("line %d: duplicate cluster slug %q (first at line %d, name=%q)", i+1, slug, prev, name))
		} else {
			slugLine[slug] = i + 1
		}
		rows = append(rows, clusterSpec{
			RowNumber:    num,
			Name:         name,
			Slug:         slug,
			EnginesProse: strings.TrimSpace(mm[6]),
		})
	}

	// 2. Intros parity: every `### <Name>` in the intros section must match a row;
	//    every row should (eventually) have an intro, but missing intros are merely
	//    informational rather than a hard failure.
	if introsIdx := strings.Index(text, "## Cluster intros"); introsIdx >= 0 {
		section := text[introsIdx:]
		if end := strings.Index(section[len("## Cluster intros"):], "\n## "); end >= 0 {
			section = section[:len("## Cluster intros")+end]
		}
		rowsByLowerName := map[string]bool{}
		for _, r := range rows {
			rowsByLowerName[strings.ToLower(strings.TrimSpace(r.Name))] = true
		}
		introsSeen := map[string]bool{}
		for _, ln := range strings.Split(section, "\n") {
			if !strings.HasPrefix(ln, "### ") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(ln, "### "))
			key := strings.ToLower(name)
			if !rowsByLowerName[key] {
				emit(fmt.Sprintf("intros section: `### %s` doesn't match any cluster row name", name))
				continue
			}
			if introsSeen[key] {
				emit(fmt.Sprintf("intros section: duplicate `### %s` heading", name))
			}
			introsSeen[key] = true
		}
	}

	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "clusters-lint: clean across %d cluster row(s)\n", len(rows))
		return
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f)
	}
	fmt.Fprintf(os.Stderr, "\nclusters-lint: %d finding(s) across %d cluster row(s)\n", len(findings), len(rows))
	os.Exit(1)
}

// numberedRow matches the start of a numbered table row — `| 1`, `| 12`, etc.
// Used to detect "this looks like a data row" so we can flag drift specifically.
var numberedRow = regexp.MustCompile(`^\|\s*\d+\s*\|`)
