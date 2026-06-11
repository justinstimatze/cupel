package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// buildDataCardJSON is the on-the-wire shape Astro consumes. It's a subset of
// workCard with the rendering layer stripped (template.HTML doesn't marshal
// usefully) and slugs surfaced so JSON consumers can build URLs.
type buildDataCardJSON struct {
	Slug       string `json:"slug"`
	Work       string `json:"work"`
	Author     string `json:"author"`
	Year       string `json:"year"`
	Medium     string `json:"medium"`
	Backing    string `json:"backing"`
	Source     string `json:"source"`
	Translator string `json:"translator,omitempty"`
	AuthorNote string `json:"author_note,omitempty"`
	Bead       string `json:"bead"`
	// BeadHTML is the bead with inline markdown (`*emphasis*`, links) rendered
	// — the card on the homepage and search-filtered list shows this so a
	// title in italics doesn't ship as a literal asterisk.
	BeadHTML    string                `json:"bead_html"`
	Engines     []buildDataEngineJSON `json:"engines"`
	HasEvidence bool                  `json:"has_evidence"`
	// ReadingHTML is the rendered HTML for the `## The reading` section, with
	// the existing renderer's linkifyWorkRefs already applied — Astro just
	// drops it into a container via set:html.
	ReadingHTML string `json:"reading_html"`
	// FullHTML is the rendered HTML for the whole body (reading + evidence)
	// — used by the per-work permalink page.
	FullHTML string `json:"full_html"`
}

type buildDataEngineJSON struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Tier     string `json:"tier"`
	Excluded bool   `json:"excluded,omitempty"`
}

type buildDataEngineSpecJSON struct {
	Name     string                    `json:"name"`
	Slug     string                    `json:"slug"`
	Tagline  string                    `json:"tagline,omitempty"`
	BodyHTML string                    `json:"body_html"`
	Works    []buildDataEngineWorkJSON `json:"works,omitempty"`
}

type buildDataEngineWorkJSON struct {
	Work string `json:"work"`
	Href string `json:"href"`
}

// runBuildData emits works.json + engines.json into --out (default
// web/src/data) so the Astro project can statically consume the catalog
// without re-parsing markdown. This is the data-pipeline half of the
// frontend migration to Astro + Tailwind + shadcn (see web/).
func runBuildData(args []string) {
	fs := flag.NewFlagSet("build-data", flag.ExitOnError)
	worksDir := fs.String("works", "works", "input directory (works/*.md) — used only for body file reads; structural data comes from --db")
	readmePath := fs.String("readme", "README.md", "README.md path (engines source)")
	counterfeitsPath := fs.String("counterfeits", "theory/counterfeit-catalog.md", "counterfeit catalog path (engine page bodies source)")
	clustersPath := fs.String("clusters", "theory/cluster-catalog.md", "cluster catalog path (clusters source)")
	glossaryPath := fs.String("glossary", "theory/glossary.md", "glossary path (glossary source)")
	linkablePath := fs.String("linkable", "theory/glossary-linkable.txt", "glossary-term auto-link allow-list (one slug per line)")
	dbPath := fs.String("db", "cupel.db", "SQLite index path (built by `cupel db-sync`)")
	outDir := fs.String("out", "web/src/data", "output directory")
	_ = fs.Parse(args)
	_ = worksDir // legacy flag; retained so existing tooling that passes it still parses.

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: mkdir %s: %v\n", *outDir, err)
		os.Exit(1)
	}

	cards, err := loadWorksFromDB(requireDB(*dbPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}
	// Build the slug→title lookup so the renderer's linkifyWorkRefs can turn
	// `works/<slug>.md` code-spans into proper italicized title links.
	workTitleBySlug := map[string]string{}
	for _, c := range cards {
		workTitleBySlug[c.Slug] = c.Work
	}
	// theoryTitleBySlug pulls the first `# ` H1 from each public theory doc so
	// front-matter related_theory entries can render the doc's prose title
	// rather than the kebab-case slug ("foundational-moment bistability" reads
	// like a leak; the doc's own H1 is the human form).
	theoryTitleBySlug := loadTheoryTitleMap("theory")
	// workNormalizedTitleToSlug keys lowercased / article-stripped titles to
	// their slug, used to resolve short italicized titles in hand-curated
	// cluster prose (`<i>Atomic Habits</i>` → /works/atomic-habits/). Colliding
	// normalized forms drop out — uniqueness keeps the linker conservative.
	workNormalizedTitleToSlug := map[string]string{}
	titleCollisions := map[string]bool{}
	for _, c := range cards {
		key := normalizeTitleForLookup(c.Work)
		if key == "" {
			continue
		}
		if prev, ok := workNormalizedTitleToSlug[key]; ok && prev != c.Slug {
			titleCollisions[key] = true
			continue
		}
		workNormalizedTitleToSlug[key] = c.Slug
	}
	for k := range titleCollisions {
		delete(workNormalizedTitleToSlug, k)
	}

	// Clusters + glossary load up-front so their stable identifiers feed both
	// (a) the per-cluster /clusters/ pages and (b) the cross-ref linker that
	// rewrites "the X cluster" / glossary aliases in dossier prose into real
	// hrefs. The catalog markdowns are the source of truth; this re-derives
	// the lookup tables on every build.
	clusters, err := loadClusters(*clustersPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}
	linkableSlugs := loadLinkableSlugs(*linkablePath)
	glossary, err := loadGlossaryWithLinkable(*glossaryPath, linkableSlugs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}
	canonicalTargets := buildClusterAndGlossaryTargets(clusters, glossary, "/cupel/")

	// linkifyAll is the per-body composition: existing work-ref pass first
	// (it removes connective prose around stripped refs, which the next pass
	// is happier reading), then italicized work-title resolution (so
	// `*The Odyssey*` in dossier prose lands on the Odyssey page — the
	// normalized-title uniqueness check keeps the pass from picking up
	// italicized engine names or generic emphasis), then the cluster/glossary
	// pass on a fresh target copy so first-occurrence tracking resets per
	// body.
	linkifyAll := func(s string) string {
		s = linkifyWorkRefs(s, workTitleBySlug, theoryTitleBySlug, "/cupel/works/", "/cupel/theory/")
		s = linkifyItalicWorkTitles(s, workNormalizedTitleToSlug, "/cupel/works/", false)
		return linkifyClustersAndGlossary(s, freshTargets(canonicalTargets))
	}

	out := make([]buildDataCardJSON, 0, len(cards))
	for _, c := range cards {
		readingHTML := linkifyAll(string(c.ReadingBody))
		// Generate the Cross-reference section from front-matter lists and
		// append to the body BEFORE linkifyAll. The renderer's linkifier then
		// passes through the generated <a href> elements unchanged (already
		// well-formed links); no strip-and-cleanup needed.
		crossRefHTML := renderCrossRefSection(c, workTitleBySlug, theoryTitleBySlug, "/cupel/")
		fullHTML := linkifyAll(string(c.FullBody) + crossRefHTML)

		engines := make([]buildDataEngineJSON, 0, len(c.Engines))
		for _, e := range c.Engines {
			engines = append(engines, buildDataEngineJSON{
				Name: e.Name, Role: e.Role, Tier: e.Tier, Excluded: e.Excluded,
			})
		}
		out = append(out, buildDataCardJSON{
			Slug: c.Slug, Work: c.Work, Author: c.Author, Year: c.Year,
			Medium: c.Medium, Backing: c.Backing, Source: c.Source,
			Translator: c.Translator,
			AuthorNote: c.AuthorNote, Bead: c.Bead,
			BeadHTML: linkifyAll(string(mdInline(c.Bead))),
			Engines:  engines, HasEvidence: c.HasEvidence,
			ReadingHTML: readingHTML, FullHTML: fullHTML,
		})
	}

	worksPath := filepath.Join(*outDir, "works.json")
	if err := writeJSON(worksPath, out); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}

	// Engines: reuse loadEngines so the Astro side gets the same per-engine
	// breakdowns (slots, counterfeit, specimens) the existing /engines/ pages
	// show. The engineSpec body is already markdown-rendered; emit it as HTML
	// and let Astro drop it in with set:html.
	specs := loadEngines(*readmePath, *counterfeitsPath, cards)
	specsOut := make([]buildDataEngineSpecJSON, 0, len(specs))
	for _, s := range specs {
		// Apply the full linkify pass so engine pages get the same cross-refs
		// as dossiers (work titles, cluster names, glossary terms).
		bodyHTML := linkifyAll(string(s.Body))
		works := make([]buildDataEngineWorkJSON, 0, len(s.Works))
		for _, w := range s.Works {
			works = append(works, buildDataEngineWorkJSON{Work: w.Work, Href: w.Href})
		}
		specsOut = append(specsOut, buildDataEngineSpecJSON{
			Name: s.Name, Slug: s.Slug, Tagline: s.Tagline,
			BodyHTML: bodyHTML, Works: works,
		})
	}
	enginesPath := filepath.Join(*outDir, "engines.json")
	if err := writeJSON(enginesPath, specsOut); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}

	// Clusters: parsed up-top so their slugs feed the cross-ref linker;
	// the JSON shape gets a rendered-HTML pair of fields per prose column so
	// the /clusters/ page can drop linked prose in via set:html. RowNumber
	// stays display-only — cross-refs use the cluster slug.
	clustersOut := make([]buildDataClusterJSON, 0, len(clusters))
	for _, c := range clusters {
		clustersOut = append(clustersOut, buildDataClusterJSON{
			RowNumber:      c.RowNumber,
			Name:           c.Name,
			Slug:           c.Slug,
			Candidate:      c.Candidate,
			Domain:         c.Domain,
			IntroHTML:      linkifyAll(string(mdInline(c.IntroMD))),
			StatusProse:    c.StatusProse,
			EnginesProse:   c.EnginesProse,
			SpecimensProse: c.SpecimensProse,
			StatusHTML:     linkifyAll(string(mdInline(c.StatusProse))),
			EnginesHTML:    linkifyAll(string(mdInline(c.EnginesProse))),
			SpecimensHTML: linkifyItalicWorkTitles(
				linkifyAll(string(mdInline(c.SpecimensProse))),
				workNormalizedTitleToSlug, "/cupel/works/", true),
		})
	}
	clustersOutPath := filepath.Join(*outDir, "clusters.json")
	if err := writeJSON(clustersOutPath, clustersOut); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}

	// Glossary: rendered entries with linker applied so cross-refs inside one
	// entry's definition become live links. Self-links are suppressed by
	// pre-marking the entry's own slug as used on the fresh target copy —
	// keeps an entry from linking its own term back to itself.
	glossaryOut := make([]buildDataGlossaryEntryJSON, 0, len(glossary))
	for _, e := range glossary {
		t := freshTargets(canonicalTargets)
		selfHref := "/cupel/glossary/#" + e.Slug
		for i := range t {
			if t[i].href == selfHref {
				t[i].used = true
			}
		}
		s := linkifyWorkRefs(e.DefinitionHTML, workTitleBySlug, theoryTitleBySlug, "/cupel/works/", "/cupel/theory/")
		s = linkifyClustersAndGlossary(s, t)
		glossaryOut = append(glossaryOut, buildDataGlossaryEntryJSON{
			Section:        e.Section,
			Term:           e.Term,
			Aliases:        e.Aliases,
			Slug:           e.Slug,
			DefinitionHTML: s,
		})
	}
	glossaryOutPath := filepath.Join(*outDir, "glossary.json")
	if err := writeJSON(glossaryOutPath, glossaryOut); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}

	// Theory docs: emit theory.json so Astro can render per-doc pages at
	// /cupel/theory/<slug>/. Without this Cross-reference + README links to
	// theory pages 404. Body is the markdown rendered via mdBlock, then
	// linkified so internal cross-refs are live.
	theoryDocs := loadTheoryDocs("theory")
	theoryOut := make([]buildDataTheoryJSON, 0, len(theoryDocs))
	for _, d := range theoryDocs {
		bodyHTML := linkifyAll(string(mdBlock(d.Body)))
		theoryOut = append(theoryOut, buildDataTheoryJSON{
			Slug: d.Slug, Title: d.Title, BodyHTML: bodyHTML,
		})
	}
	theoryOutPath := filepath.Join(*outDir, "theory.json")
	if err := writeJSON(theoryOutPath, theoryOut); err != nil {
		fmt.Fprintf(os.Stderr, "build-data: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("build-data: wrote %s (%d works) + %s (%d engines) + %s (%d clusters) + %s (%d glossary) + %s (%d theory)\n",
		strings.TrimPrefix(worksPath, "./"), len(out),
		strings.TrimPrefix(enginesPath, "./"), len(specsOut),
		strings.TrimPrefix(clustersOutPath, "./"), len(clustersOut),
		strings.TrimPrefix(glossaryOutPath, "./"), len(glossaryOut),
		strings.TrimPrefix(theoryOutPath, "./"), len(theoryOut))
}

// buildDataTheoryJSON is the on-the-wire shape Astro consumes for per-theory
// pages. Title is pulled from the first H1; body is the full doc minus the
// H1 line, rendered via the same mdBlock + linkify pipeline as dossiers.
type buildDataTheoryJSON struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	BodyHTML string `json:"body_html"`
}

// theoryDoc is the in-memory shape for a public theory document.
type theoryDoc struct {
	Slug  string
	Title string
	Body  string
}

// loadTheoryDocs walks theory/*.md (skipping session-handoff scratchpads and
// the gitignored theory/working/ tree) and returns each as a theoryDoc with
// the leading H1 lifted into Title. The body is the rest of the doc.
func loadTheoryDocs(theoryDir string) []theoryDoc {
	files, _ := filepath.Glob(filepath.Join(theoryDir, "*.md"))
	out := []theoryDoc{}
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") || strings.HasPrefix(base, "_") || base == "README.md" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.ReplaceAll(string(data), "\r", "")
		slug := strings.TrimSuffix(base, ".md")
		title, body := splitH1Title(text)
		if title == "" {
			title = slug
		}
		out = append(out, theoryDoc{Slug: slug, Title: title, Body: body})
	}
	return out
}

// splitH1Title pulls the first `# Title` line out of the doc and returns it
// + everything after. If no H1 found, returns ("", original text).
func splitH1Title(text string) (title, body string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			body = strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n")
			return
		}
	}
	return "", text
}

// buildDataClusterJSON is the on-the-wire shape Astro consumes for clusters.
// StatusProse/EnginesProse/SpecimensProse remain as text fallbacks; the
// rendered HTML pair runs the same linkify pass the dossiers do, so cluster
// pages get cross-refs to the same target set.
type buildDataClusterJSON struct {
	RowNumber      int    `json:"row_number"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Candidate      bool   `json:"candidate,omitempty"`
	Domain         string `json:"domain"`
	IntroHTML      string `json:"intro_html,omitempty"`
	StatusProse    string `json:"status_prose"`
	EnginesProse   string `json:"engines_prose"`
	SpecimensProse string `json:"specimens_prose"`
	StatusHTML     string `json:"status_html"`
	EnginesHTML    string `json:"engines_html"`
	SpecimensHTML  string `json:"specimens_html"`
}

type buildDataGlossaryEntryJSON struct {
	Section        string   `json:"section"`
	Term           string   `json:"term"`
	Aliases        []string `json:"aliases,omitempty"`
	Slug           string   `json:"slug"`
	DefinitionHTML string   `json:"definition_html"`
}

// renderCrossRefSection turns the dossier's front-matter related_works /
// related_theory / pending_refs lists into a Cross-reference section HTML
// fragment appended to the body. Empty when all three lists are empty.
// Public targets (related_*) resolve to live <a href> links; pending_refs
// render as italicized titles with a "(pending)" badge.
func renderCrossRefSection(c workCard, workTitleBySlug, theoryTitleBySlug map[string]string, linkPrefix string) string {
	if len(c.RelatedWorks) == 0 && len(c.RelatedTheory) == 0 && len(c.PendingRefs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<h3>Cross-reference</h3>\n<ul>\n")
	for _, r := range c.RelatedWorks {
		title := workTitleBySlug[r.Slug]
		if title == "" {
			title = r.Slug
		}
		b.WriteString(fmt.Sprintf(`<li><a href="%sworks/%s/">%s</a> — %s.</li>`+"\n",
			linkPrefix, r.Slug, htmlEscape(title), htmlEscape(r.Gloss)))
	}
	for _, r := range c.RelatedTheory {
		title := theoryTitleBySlug[r.Slug]
		if title == "" {
			title = r.Slug
		}
		b.WriteString(fmt.Sprintf(`<li><a href="%stheory/%s/">%s</a> — %s.</li>`+"\n",
			linkPrefix, r.Slug, htmlEscape(title), htmlEscape(r.Gloss)))
	}
	for _, r := range c.PendingRefs {
		b.WriteString(fmt.Sprintf(`<li><i>%s</i> <span class="text-dim text-[12px]">(pending)</span> — %s.</li>`+"\n",
			htmlEscape(r.Slug), htmlEscape(r.Gloss)))
	}
	b.WriteString("</ul>\n")
	return b.String()
}

// loadTheoryTitleMap walks theory/*.md (excluding session-handoff scratchpads)
// and pulls each doc's first H1 (the "# Title" line) as its display title.
// Falls back to the slug if the file has no H1 or can't be read.
func loadTheoryTitleMap(theoryDir string) map[string]string {
	out := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(theoryDir, "*.md"))
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") {
			continue
		}
		slug := strings.TrimSuffix(base, ".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") {
				title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
				// Theory H1s often have the shape "Title — subtitle"; the
				// subtitle is useful at the doc's own page but reads heavy
				// in a cross-reference link. Keep only the part before the
				// first em-dash.
				if i := strings.Index(title, " — "); i > 0 {
					title = title[:i]
				}
				out[slug] = title
				break
			}
		}
	}
	return out
}

// htmlEscape is a thin wrapper to avoid pulling html/template here.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	s = strings.ReplaceAll(s, `'`, "&#39;")
	return s
}

func writeJSON(path string, v interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return nil
}
