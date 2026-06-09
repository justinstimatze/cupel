package main

// migrate_refs.go — one-shot YAML-front-matter migration helper.
//
// Pass 1 (this file, minimum-viable): in each works/*.md body, rewrite inline
// code-span cross-refs (`works/SLUG.md` and `theory/SLUG.md`) and Obsidian
// wikilinks ([[SLUG]]) into proper markdown links *when the target exists on
// the public site* (i.e. a real `works/SLUG.md` or one of the public theory
// docs). Targets that resolve to non-existent dossiers or to private paths
// (NOTES.md, cmd/, theory/working/) are left untouched — the existing
// renderer strip-and-cleanup chain still handles them.
//
// Goal: kill the largest source of whack-a-mole (orphan-pointer cleanup
// around known-public refs) without committing to the full substrate
// refactor yet. After this pass, the next builds should not produce any
// dangling bullets or surrounding-prose artifacts around resolvable refs.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	inlineCodeSpanRef = regexp.MustCompile("`(works|theory)/([a-z0-9-]+)\\.md`")
	wikilinkRef       = regexp.MustCompile(`\[\[([a-z0-9-]+)\]\]`)
	// crossRefMDLinkPrefix matches a bullet starting with a Pass-1 markdown
	// link: `- [Title](/cupel/(works|theory)/SLUG/)`. The text after this
	// prefix (everything up to the first em-dash) may contain compound link
	// runs ("[A], [B]"); we only record the leading slug + the gloss.
	crossRefMDLinkPrefix = regexp.MustCompile(`^-\s+\[[^\]]+\]\(/cupel/(works|theory)/([a-z0-9-]+)/\)`)
	// crossRefInlinePrefix matches a bullet whose leading ref is still in
	// inline-code form — the not-yet-dossiered case (`works/SLUG.md`).
	crossRefInlinePrefix = regexp.MustCompile("^-\\s+`(works|theory)/([a-z0-9-]+)\\.md`")
)

func runMigrateRefs(args []string) {
	fs := flag.NewFlagSet("migrate-refs", flag.ExitOnError)
	worksDir := fs.String("works", "works", "works/ directory")
	theoryDir := fs.String("theory", "theory", "theory/ directory")
	linkPrefix := fs.String("link-prefix", "/cupel", "URL prefix for generated links")
	dryRun := fs.Bool("dry-run", false, "show summary without writing files")
	mode := fs.String("mode", "inline-links", "inline-links (Pass 1: convert inline code-spans) | cross-ref (Pass 2: ### Cross-reference → front-matter)")
	_ = fs.Parse(args)

	if *mode == "cross-ref" {
		runMigrateCrossRef(*worksDir, *dryRun)
		return
	}

	workTitleBySlug, err := loadWorkTitleMap(*worksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-refs: %v\n", err)
		os.Exit(1)
	}
	theorySlugs, err := loadPublicTheorySlugs(*theoryDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-refs: %v\n", err)
		os.Exit(1)
	}

	files, err := filepath.Glob(filepath.Join(*worksDir, "*.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-refs: %v\n", err)
		os.Exit(1)
	}

	filesChanged := 0
	refsConverted := 0
	wikilinksConverted := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate-refs: read %s: %v\n", path, err)
			continue
		}
		frontMatter, body, ok := splitRawFrontMatter(string(data))
		if !ok {
			// No front-matter — skip (works/*.md should all have it).
			continue
		}
		newBody, n1 := convertCodeSpanRefs(body, workTitleBySlug, theorySlugs, *linkPrefix)
		newBody, n2 := convertWikilinks(newBody, workTitleBySlug, *linkPrefix)
		if n1 == 0 && n2 == 0 {
			continue
		}
		filesChanged++
		refsConverted += n1
		wikilinksConverted += n2
		if *dryRun {
			continue
		}
		out := frontMatter + newBody
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "migrate-refs: write %s: %v\n", path, err)
			os.Exit(1)
		}
	}
	fmt.Printf("migrate-refs: %d files touched, %d code-span refs converted, %d wikilinks converted (dry-run=%v)\n",
		filesChanged, refsConverted, wikilinksConverted, *dryRun)
}

// splitRawFrontMatter returns (frontmatter-including-fences, body, ok). Front
// matter is the leading `---\n...\n---\n` block; body is everything after.
func splitRawFrontMatter(s string) (front, body string, ok bool) {
	if !strings.HasPrefix(s, "---\n") {
		return "", s, false
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", s, false
	}
	return s[:4+end+5], rest[end+5:], true
}

// loadWorkTitleMap reads each works/*.md front-matter and pulls the `work:`
// field as the display title for that slug.
func loadWorkTitleMap(worksDir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(worksDir, "*.md"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, path := range files {
		slug := strings.TrimSuffix(filepath.Base(path), ".md")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		front, _, ok := splitRawFrontMatter(string(data))
		if !ok {
			continue
		}
		title := extractYAMLField(front, "work")
		if title == "" {
			title = slug
		}
		out[slug] = title
	}
	return out, nil
}

// loadPublicTheorySlugs walks theory/ and returns the slugs of theory docs
// that are public (excludes session-handoff*, loop-session-handoff*, and
// the gitignored theory/working/ tree).
func loadPublicTheorySlugs(theoryDir string) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(theoryDir, "*.md"))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") {
			continue
		}
		slug := strings.TrimSuffix(base, ".md")
		out[slug] = true
	}
	return out, nil
}

// extractYAMLField pulls the value of `key: ...` from a front-matter block.
// Tolerates quoted and unquoted scalars; does not handle multi-line YAML
// values (none of the dossiers use them for the `work:` field).
func extractYAMLField(front, key string) string {
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
		return v
	}
	return ""
}

// convertCodeSpanRefs rewrites `works/SLUG.md` → [title](/cupel/works/SLUG/)
// and `theory/SLUG.md` → [slug](/cupel/theory/SLUG/) in body prose, but ONLY
// when SLUG resolves to a known public target. Unknown slugs are left alone
// so the existing renderer chain continues to handle them.
func convertCodeSpanRefs(body string, workTitleBySlug map[string]string, theorySlugs map[string]bool, linkPrefix string) (string, int) {
	count := 0
	out := inlineCodeSpanRef.ReplaceAllStringFunc(body, func(match string) string {
		mm := inlineCodeSpanRef.FindStringSubmatch(match)
		kind, slug := mm[1], mm[2]
		switch kind {
		case "works":
			title, ok := workTitleBySlug[slug]
			if !ok {
				return match
			}
			count++
			return fmt.Sprintf("[%s](%s/works/%s/)", escapeMDLinkText(title), linkPrefix, slug)
		case "theory":
			if !theorySlugs[slug] {
				return match
			}
			count++
			return fmt.Sprintf("[%s](%s/theory/%s/)", slug, linkPrefix, slug)
		}
		return match
	})
	return out, count
}

// convertWikilinks rewrites [[slug]] → [title](/cupel/works/slug/) when slug
// matches a known work. Theory wikilinks would be unusual (catalog convention
// is bracketed-code for theory refs); we don't attempt them here.
func convertWikilinks(body string, workTitleBySlug map[string]string, linkPrefix string) (string, int) {
	count := 0
	out := wikilinkRef.ReplaceAllStringFunc(body, func(match string) string {
		mm := wikilinkRef.FindStringSubmatch(match)
		slug := mm[1]
		title, ok := workTitleBySlug[slug]
		if !ok {
			return match
		}
		count++
		return fmt.Sprintf("[%s](%s/works/%s/)", escapeMDLinkText(title), linkPrefix, slug)
	})
	return out, count
}

// escapeMDLinkText escapes square brackets inside a markdown link's text
// portion. Catalog work titles use square brackets rarely — but a few cite
// chapter/line markers — so this protects the link syntax.
func escapeMDLinkText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	return s
}

// --- Pass 2: ### Cross-reference section → front-matter ---

// runMigrateCrossRef walks works/*.md, finds dossiers with an explicit
// "### Cross-reference" section, parses each bullet into a structured entry,
// emits the entries as YAML lists into the front-matter, and drops the section
// from the body. The renderer (Pass 3) reads the front-matter lists and
// regenerates the section. After Pass 2 the dossier MDs no longer carry the
// section in prose — the substrate is canonical.
func runMigrateCrossRef(worksDir string, dryRun bool) {
	files, err := filepath.Glob(filepath.Join(worksDir, "*.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-refs (cross-ref): %v\n", err)
		os.Exit(1)
	}
	filesChanged := 0
	totalEntries := 0
	totalDanglers := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate-refs (cross-ref): read %s: %v\n", path, err)
			continue
		}
		text := string(data)
		newText, works, theory, pending, danglers, ok := transformCrossRef(text)
		if !ok {
			continue
		}
		if len(works)+len(theory)+len(pending) == 0 && danglers == 0 {
			continue
		}
		filesChanged++
		totalEntries += len(works) + len(theory) + len(pending)
		totalDanglers += danglers
		if dryRun {
			continue
		}
		if err := os.WriteFile(path, []byte(newText), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "migrate-refs (cross-ref): write %s: %v\n", path, err)
			os.Exit(1)
		}
	}
	fmt.Printf("migrate-refs (cross-ref): %d files touched, %d entries migrated, %d danglers dropped (dry-run=%v)\n",
		filesChanged, totalEntries, totalDanglers, dryRun)
}

// transformCrossRef does the structural rewrite for one dossier text: split
// front-matter from body, locate the ### Cross-reference section in the body,
// parse its bullets, inject the entries into the front-matter as YAML lists,
// and remove the section from the body. Returns the rewritten text plus the
// classification counts. ok=false means the file has no front-matter and was
// skipped.
func transformCrossRef(text string) (newText string, works, theory, pending []crossRefEntry, danglers int, ok bool) {
	front, body, fmOK := splitRawFrontMatter(text)
	if !fmOK {
		return "", nil, nil, nil, 0, false
	}
	// Locate the heading; if not present, nothing to do.
	const heading = "### Cross-reference"
	lines := strings.Split(body, "\n")
	headIdx := -1
	for i, ln := range lines {
		if strings.TrimRight(ln, " \t") == heading {
			headIdx = i
			break
		}
	}
	if headIdx < 0 {
		return "", nil, nil, nil, 0, true // no section, but still ok=true
	}
	// Walk lines after the heading to find the section end. The section
	// extends through contiguous bullets + optional blank-line separators;
	// it ends at the first non-bullet non-blank line (another heading, the
	// closing `---` horizontal rule, or arbitrary prose).
	end := headIdx + 1
	for end < len(lines) {
		ln := strings.TrimSpace(lines[end])
		if ln == "" || strings.HasPrefix(ln, "- ") {
			end++
			continue
		}
		break
	}
	// Classify each bullet in [headIdx+1, end).
	for i := headIdx + 1; i < end; i++ {
		ln := strings.TrimRight(lines[i], " \t")
		if !strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			continue
		}
		// Gloss extraction has to skip past the leading link's syntax — em-dashes
		// inside the link's title text (e.g. "[Foo — Subtitle](...)") would
		// otherwise be mistaken for the gloss separator. Find the prefix match
		// first, then search for the em-dash from AFTER the prefix.
		if loc := crossRefMDLinkPrefix.FindStringIndex(ln); loc != nil {
			gloss := extractGlossAfterIndex(ln, loc[1])
			mm := crossRefMDLinkPrefix.FindStringSubmatch(ln)
			kind, slug := mm[1], mm[2]
			entry := crossRefEntry{Slug: slug, Gloss: gloss}
			if kind == "works" {
				works = append(works, entry)
			} else {
				theory = append(theory, entry)
			}
			continue
		}
		if loc := crossRefInlinePrefix.FindStringIndex(ln); loc != nil {
			gloss := extractGlossAfterIndex(ln, loc[1])
			mm := crossRefInlinePrefix.FindStringSubmatch(ln)
			_, slug := mm[1], mm[2]
			pending = append(pending, crossRefEntry{Slug: slug, Gloss: gloss})
			continue
		}
		danglers++
	}
	// Rebuild body without the section. Trim a single trailing blank line
	// inside the heading...end window so we don't leave double-blank.
	cut := end
	if cut > headIdx+1 && strings.TrimSpace(lines[cut-1]) == "" {
		// keep the blank line — it belongs to whatever follows
	}
	newBodyLines := append([]string{}, lines[:headIdx]...)
	// drop one preceding blank line if the heading was preceded by blank+
	// section to avoid leaving \n\n\n. The trailing-after region keeps its
	// own leading blank if it had one.
	if len(newBodyLines) > 0 && strings.TrimSpace(newBodyLines[len(newBodyLines)-1]) == "" {
		newBodyLines = newBodyLines[:len(newBodyLines)-1]
	}
	newBodyLines = append(newBodyLines, lines[cut:]...)
	newBody := strings.Join(newBodyLines, "\n")
	newFront := injectCrossRefFrontMatter(front, works, theory, pending)
	return newFront + newBody, works, theory, pending, danglers, true
}

// extractGlossAfterIndex finds the em-dash separator at or after `start` and
// returns the prose that follows. The em-dash glyph (—, U+2014) is the catalog
// convention for the link-gloss separator. ASCII hyphens appear inside refs
// and glosses ("pure-counterfeit") and are NOT separators. `start` lets the
// caller skip past a leading link whose title text may itself contain an
// em-dash. Returns "" if no em-dash is found at or after start.
func extractGlossAfterIndex(line string, start int) string {
	rel := strings.Index(line[start:], "—")
	if rel < 0 {
		return ""
	}
	gloss := strings.TrimSpace(line[start+rel+len("—"):])
	gloss = strings.TrimSuffix(gloss, ".")
	return strings.TrimSpace(gloss)
}

// crossRefEntry — one Cross-reference bullet's structured form.
type crossRefEntry struct {
	Slug  string
	Gloss string
}

// injectCrossRefFrontMatter appends related_works:, related_theory:,
// pending_refs: lists to the front-matter block. The block keeps its existing
// fields and closing fence; lists are inserted just before the fence.
func injectCrossRefFrontMatter(front string, works, theory, pending []crossRefEntry) string {
	if len(works)+len(theory)+len(pending) == 0 {
		return front
	}
	// front ends with "---\n"; insert before that closing fence.
	const fence = "---\n"
	idx := strings.LastIndex(front, fence)
	if idx < 0 {
		return front
	}
	var b strings.Builder
	b.WriteString(front[:idx])
	writeRefList := func(name string, entries []crossRefEntry) {
		if len(entries) == 0 {
			return
		}
		b.WriteString(name + ":\n")
		for _, e := range entries {
			gloss := strings.ReplaceAll(e.Gloss, `"`, `\"`)
			b.WriteString(fmt.Sprintf("  - %s :: %s\n", e.Slug, gloss))
		}
	}
	writeRefList("related_works", works)
	writeRefList("related_theory", theory)
	writeRefList("pending_refs", pending)
	b.WriteString(front[idx:])
	return b.String()
}
