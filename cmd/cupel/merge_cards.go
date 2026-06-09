package main

// merge_cards.go — `cupel merge-cards`: one-shot migration from the two-file
// (reviews/<slug>.md + entries/<slug>.md) model to single works/<slug>.md
// pages. The two-file model was path-dependent — reviews came first; entries
// arrived once the slot-test discipline hardened — and maintaining both in
// parallel taxes every cross-cutting edit. One file per work, with the review
// as an executive summary at the top under "## The reading" and the dossier
// as verbatim evidence underneath under "## The evidence". Pairing is by
// filename slug (== basename without .md); the entry: front-matter field is
// validated but not used for matching.
//
// Dry-run by default; --write emits to --outdir (default works/). The output
// can be re-validated by re-running tag-audit / quotes-audit on it.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// fmKeyOrder defines the canonical front-matter field order in merged files.
// Anything in the union of review/entry front-matter is emitted in this order;
// unknown extras fall through at the end alphabetically.
var fmKeyOrder = []string{
	"work", "author", "year", "medium", "backing",
	"source",
	"engines", "layer", "verified", "status",
	"author_note",
}

// dropFrontMatterKeys are fields that don't belong in the merged file.
// `entry` named the link from review→entry; the merged file IS the entry,
// so the link is meaningless.
var dropFrontMatterKeys = map[string]bool{
	"entry": true,
}

// fmRawLine captures "key: value" exactly as it appears on disk, preserving
// arbitrary value content (engine arrays, quoted strings, etc.) — splitFrontMatter
// in render.go strips quotes and normalizes whitespace, which is wrong for
// round-tripping. We re-parse from raw text here so the values written into
// works/<slug>.md match the source byte-for-byte.
var fmRawLine = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*):\s*(.*?)\s*$`)

// rawFrontMatter parses the file's front-matter preserving raw values, and
// returns the body that follows. Unlike splitFrontMatter, this doesn't strip
// surrounding quotes — works/<slug>.md should serialize values verbatim.
func rawFrontMatter(text string) (map[string]string, []string, string) {
	fm := map[string]string{}
	var keys []string
	body := text
	if !strings.HasPrefix(text, "---") {
		return fm, keys, body
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return fm, keys, body
	}
	block := text[3 : 3+end]
	body = text[3+end+4:]
	for _, ln := range strings.Split(block, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		mm := fmRawLine.FindStringSubmatch(t)
		if mm == nil {
			continue
		}
		k, v := mm[1], mm[2]
		if _, seen := fm[k]; !seen {
			keys = append(keys, k)
		}
		fm[k] = v
	}
	return fm, keys, body
}

// stripLeadingH1 removes the file's first H1 and any blank lines around it,
// so the merged file uses the work-title H1 from the frontmatter and the
// review/entry bodies start at their first real paragraph.
func stripLeadingH1(body string) string {
	body = strings.TrimLeft(body, "\n")
	if !strings.HasPrefix(body, "# ") {
		return body
	}
	if nl := strings.Index(body, "\n"); nl >= 0 {
		return strings.TrimLeft(body[nl+1:], "\n")
	}
	return ""
}

// demoteHeadings shifts every "## " / "### " / "#### " heading down one level,
// so the entry's section structure nests under "## The evidence" rather than
// becoming siblings of it. H1 isn't expected at this point (stripped above).
// Lines starting with > or in code fences are left alone — those aren't
// headings even when they begin with #.
var headingLine = regexp.MustCompile(`(?m)^(#{2,5})\s+`)

func demoteHeadings(body string) string {
	inFence := false
	lines := strings.Split(body, "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if mm := headingLine.FindStringSubmatch(ln); mm != nil {
			lines[i] = "#" + ln
		}
	}
	return strings.Join(lines, "\n")
}

// mergeFrontMatter unions review + entry frontmatter. Review values win on
// collision (review is the product-tier metadata; entry is lab-notebook).
// `entry:` is dropped (file IS the entry). Output is emitted in fmKeyOrder
// with unknown extras appended alphabetically.
func mergeFrontMatter(rev, ent map[string]string) string {
	merged := map[string]string{}
	for k, v := range ent {
		if dropFrontMatterKeys[k] || v == "" {
			continue
		}
		merged[k] = v
	}
	for k, v := range rev {
		if dropFrontMatterKeys[k] || v == "" {
			continue
		}
		merged[k] = v
	}

	known := map[string]bool{}
	for _, k := range fmKeyOrder {
		known[k] = true
	}
	var extras []string
	for k := range merged {
		if !known[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)

	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range fmKeyOrder {
		if v, ok := merged[k]; ok {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	for _, k := range extras {
		fmt.Fprintf(&b, "%s: %s\n", k, merged[k])
	}
	b.WriteString("---\n")
	return b.String()
}

// mergeBody assembles "## The reading" + "## The evidence" sections from the
// two source bodies. Either may be empty (review-only or evidence-only works).
func mergeBody(revBody, entBody string) string {
	revBody = stripLeadingH1(revBody)
	entBody = stripLeadingH1(entBody)
	entBody = demoteHeadings(entBody)

	var b strings.Builder
	b.WriteString("\n")
	if revBody != "" {
		b.WriteString("## The reading\n\n")
		b.WriteString(strings.TrimRight(revBody, "\n"))
		b.WriteString("\n")
	}
	if entBody != "" {
		if revBody != "" {
			b.WriteString("\n")
		}
		b.WriteString("## The evidence\n\n")
		b.WriteString(strings.TrimRight(entBody, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// rawCard is what we read from a single source file before merging.
type rawCard struct {
	Slug     string
	FmKeys   []string
	Fm       map[string]string
	Body     string
	Filename string
}

func loadRaw(dir string) map[string]rawCard {
	out := map[string]rawCard{}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasPrefix(base, "_") {
			continue
		}
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		text := strings.ReplaceAll(string(b), "\r", "")
		fm, keys, body := rawFrontMatter(text)
		if fm["work"] == "" {
			continue
		}
		slug := strings.TrimSuffix(base, ".md")
		out[slug] = rawCard{
			Slug: slug, FmKeys: keys, Fm: fm, Body: body, Filename: m,
		}
	}
	return out
}

type mergePlan struct {
	Slug       string
	Pair       bool // both review + entry
	ReviewOnly bool
	EntryOnly  bool // orphan dossier
	Content    string
}

func planMerges(reviewsDir, entriesDir string) []mergePlan {
	reviews := loadRaw(reviewsDir)
	entries := loadRaw(entriesDir)

	slugs := map[string]bool{}
	for s := range reviews {
		slugs[s] = true
	}
	for s := range entries {
		slugs[s] = true
	}
	var order []string
	for s := range slugs {
		order = append(order, s)
	}
	sort.Strings(order)

	var plans []mergePlan
	for _, slug := range order {
		rev, hasR := reviews[slug]
		ent, hasE := entries[slug]
		fm := mergeFrontMatter(map[string]string{}, map[string]string{})
		var body string
		switch {
		case hasR && hasE:
			fm = mergeFrontMatter(rev.Fm, ent.Fm)
			body = mergeBody(rev.Body, ent.Body)
		case hasR:
			fm = mergeFrontMatter(rev.Fm, nil)
			body = mergeBody(rev.Body, "")
		case hasE:
			fm = mergeFrontMatter(nil, ent.Fm)
			body = mergeBody("", ent.Body)
		}
		plans = append(plans, mergePlan{
			Slug:       slug,
			Pair:       hasR && hasE,
			ReviewOnly: hasR && !hasE,
			EntryOnly:  !hasR && hasE,
			Content:    fm + body,
		})
	}
	return plans
}

func runMergeCards(args []string) {
	fs := flag.NewFlagSet("merge-cards", flag.ContinueOnError)
	reviewsDir := fs.String("reviews", "reviews", "directory of reviews/*.md")
	entriesDir := fs.String("entries", "entries", "directory of entries/*.md")
	outdir := fs.String("outdir", "works", "output directory for merged works/*.md")
	write := fs.Bool("write", false, "write files to --outdir; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return
	}

	plans := planMerges(*reviewsDir, *entriesDir)
	var paired, reviewOnly, entryOnly int
	for _, p := range plans {
		switch {
		case p.Pair:
			paired++
		case p.ReviewOnly:
			reviewOnly++
		case p.EntryOnly:
			entryOnly++
		}
	}
	fmt.Fprintf(os.Stderr,
		"merge-cards: %d total · %d paired · %d review-only · %d evidence-only (orphan dossiers)\n",
		len(plans), paired, reviewOnly, entryOnly)

	if !*write {
		fmt.Fprintln(os.Stderr, "merge-cards: dry-run — re-run with --write to emit files")
		// Surface orphan dossiers so the user can see what'll land evidence-only.
		var orphans []string
		for _, p := range plans {
			if p.EntryOnly {
				orphans = append(orphans, p.Slug)
			}
		}
		if len(orphans) > 0 {
			fmt.Fprintln(os.Stderr, "orphan dossiers (entry without matching review):")
			for _, s := range orphans {
				fmt.Fprintln(os.Stderr, "  ", s)
			}
		}
		return
	}

	if err := os.MkdirAll(*outdir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "merge-cards:", err)
		os.Exit(1)
	}
	var failures int
	for _, p := range plans {
		path := filepath.Join(*outdir, p.Slug+".md")
		if err := os.WriteFile(path, []byte(p.Content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "merge-cards:", path+":", err)
			failures++
		}
	}
	fmt.Fprintf(os.Stderr, "merge-cards: wrote %d files to %s/\n", len(plans)-failures, *outdir)
	if failures > 0 {
		os.Exit(1)
	}
}
