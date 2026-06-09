package main

// works_lint.go — `cupel works-lint`: validates the markdown-as-schema
// convention for works/*.md. The shape that the renderer expects (and the
// merge-cards subcommand produces) is convention-based, not enforced by a
// schema; this subcommand turns that convention into a CI gate so drift can't
// land silently. The renderer keeps its forgiving "best effort" mode — works-lint
// makes the silent drops visible at commit time.
//
// Checks per file (each fires its own line of output; exit 1 on any failure):
//   - frontmatter present with `work` + `author` + `backing` keys
//   - body has a `## The reading` heading
//   - inside the reading, a `**The bead.**` paragraph
//   - at least one engine bullet: `- **<name>** · <layer> · <role> · ✓|~`
//   - when `backing: slot-proven`, a `## The evidence` heading is present
//
// Engine-name canonicality is enforced separately by `tag-audit`; this
// subcommand only validates structural shape.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// inlineRefInBody scans dossier body prose for `(works|theory)/SLUG.md`
// code-span refs that should now live in front-matter as related_works /
// related_theory entries. Catches authoring regressions after the 2026-06-07
// migration — without it, a careless inline ref slips back into prose and
// re-introduces the whack-a-mole the strip-and-cleanup chain was retired to
// stop fighting.
var inlineRefInBody = regexp.MustCompile("`(works|theory)/([a-z0-9-]+)\\.md`")

type lintFinding struct {
	File   string
	Reason string
}

func runWorksLint(args []string) {
	fs := flag.NewFlagSet("works-lint", flag.ContinueOnError)
	dir := fs.String("dir", "works", "directory of works/*.md to lint")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	matches, _ := filepath.Glob(filepath.Join(*dir, "*.md"))
	if len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "works-lint: no .md files in", *dir)
		os.Exit(1)
	}

	// Build the known-work + known-theory sets so the inline-ref check can tell
	// "this references a real public dossier" (which should be in front-matter,
	// not body prose) from "this references a not-yet-written work" (which
	// belongs in pending_refs in front-matter).
	knownWorks := map[string]bool{}
	for _, m := range matches {
		slug := strings.TrimSuffix(filepath.Base(m), ".md")
		if !strings.HasPrefix(slug, "_") {
			knownWorks[slug] = true
		}
	}
	knownTheory := map[string]bool{}
	theoryFiles, _ := filepath.Glob("theory/*.md")
	for _, t := range theoryFiles {
		base := filepath.Base(t)
		if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") {
			continue
		}
		knownTheory[strings.TrimSuffix(base, ".md")] = true
	}

	var findings []lintFinding
	scanned := 0
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, "_") {
			continue
		}
		b, err := os.ReadFile(m)
		if err != nil {
			findings = append(findings, lintFinding{File: m, Reason: "read: " + err.Error()})
			continue
		}
		scanned++
		findings = append(findings, lintFile(m, string(b), knownWorks, knownTheory)...)
	}

	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "works-lint: clean across %d file(s)\n", scanned)
		return
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "%s — %s\n", f.File, f.Reason)
	}
	fmt.Fprintf(os.Stderr, "\nworks-lint: %d finding(s) across %d file(s) scanned\n", len(findings), scanned)
	os.Exit(1)
}

func lintFile(path, text string, knownWorks, knownTheory map[string]bool) []lintFinding {
	text = strings.ReplaceAll(text, "\r", "")
	fm, body, ferr := splitFrontMatter(text)
	var out []lintFinding

	// yaml.v3 strict-decode catches unknown keys (typos like `bakcing:`) and
	// malformed YAML — surface as a lint finding so the commit fails before
	// a silent drop downstream.
	if ferr != nil {
		out = append(out, lintFinding{File: path, Reason: "frontmatter parse: " + ferr.Error()})
		return out
	}

	required := []struct {
		name  string
		value string
	}{{"work", fm.Work}, {"author", fm.Author}, {"backing", fm.Backing}}
	for _, r := range required {
		if r.value == "" {
			out = append(out, lintFinding{File: path, Reason: "frontmatter missing or empty: " + r.name})
		}
	}

	if !strings.Contains(body, "## The reading") {
		out = append(out, lintFinding{File: path, Reason: "missing `## The reading` heading"})
	}

	reading, hasEvidence := splitWorkBody(body)
	if !strings.Contains(reading, "**The bead.**") {
		out = append(out, lintFinding{File: path, Reason: "missing `**The bead.**` paragraph in the reading"})
	}

	if engineLine.FindStringSubmatch(reading) == nil {
		out = append(out, lintFinding{File: path, Reason: "no engine bullet matched (`- **name** · layer · role · ✓|~`)"})
	}

	if fm.Backing == "slot-proven" && !hasEvidence {
		out = append(out, lintFinding{File: path, Reason: "backing: slot-proven but no `## The evidence` heading"})
	}

	// Inline cross-ref policy (post-2026-06-07 migration): cross-refs go in
	// front-matter (related_works:, related_theory:, pending_refs:). Body prose
	// shouldn't carry `(works|theory)/SLUG.md` code-spans — known or unknown.
	// Flag every hit; the author moves the ref to front-matter or removes it.
	for _, mm := range inlineRefInBody.FindAllStringSubmatch(body, -1) {
		kind, slug := mm[1], mm[2]
		var reason string
		switch {
		case kind == "works" && knownWorks[slug]:
			reason = fmt.Sprintf("body has inline ref `works/%s.md` — move to front-matter `related_works:` (target exists)", slug)
		case kind == "works":
			reason = fmt.Sprintf("body has inline ref `works/%s.md` — target not yet dossiered; move to front-matter `pending_refs:` or remove", slug)
		case kind == "theory" && knownTheory[slug]:
			reason = fmt.Sprintf("body has inline ref `theory/%s.md` — move to front-matter `related_theory:` (target exists)", slug)
		default:
			reason = fmt.Sprintf("body has inline ref `theory/%s.md` — target not public; remove or relocate to a public theory doc", slug)
		}
		out = append(out, lintFinding{File: path, Reason: reason})
	}

	return out
}
