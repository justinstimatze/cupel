package main

// parse.go — the data layer behind `cupel build-data` (and the other
// catalog-reading subcommands: works-lint, tag-audit, coverage, merge-cards).
// Walks works/*.md (one merged file per work: review-style summary under
// `## The reading`, dossier evidence under `## The evidence` when slot-proven),
// parses each file into a workCard, and renders the bodies to HTML via a
// dependency-free markdown subset. The README's `## The engines` section
// is parsed analogously into engineSpec values.
//
// Was render.go through 2026-06-06 (the HTML-rendering layer); render.go +
// serve.go were deleted at the Astro cutover. The data layer survives because
// the JSON shape emitted by build-data is what the Astro frontend consumes.

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type engineTag struct {
	Name, Role, Tier string // Tier: "✓" slot-proven | "~" reviewed
	Excluded         bool
}

type workCard struct {
	Work, Author, Year, Medium, Backing, Source string
	Translator                                  string // frontmatter `translator:` — credit for translated editions (e.g. Hapgood 1888 for Notre-Dame de Paris); rendered as "· trans. X" next to the author line when present.
	AuthorNote                                  string // frontmatter `author_note:` — surfaces as a ⚠ badge for well-documented author-conduct concerns; the card is then a case-study, not platforming.
	Slug                                        string
	Bead                                        string
	Engines                                     []engineTag
	ReadingBody                                 template.HTML // just the `## The reading` section, rendered (for the index card's expandable disclosure)
	FullBody                                    template.HTML // the whole work (reading + evidence), rendered (for the per-work page)
	HasEvidence                                 bool          // true iff body contains a `## The evidence` section
	File                                        string
	RelatedWorks                                []relatedRef // frontmatter list `related_works: [- slug :: gloss]`; renderer generates Cross-reference HTML from these.
	RelatedTheory                               []relatedRef // same for `related_theory:`
	PendingRefs                                 []relatedRef // same for `pending_refs:` — cross-refs to not-yet-existing dossiers; rendered greyed-out.
}

// relatedRef is one parsed `  - slug :: gloss` entry under a related_works /
// related_theory / pending_refs key in the front-matter. The slug is the
// cross-ref target; the gloss is the editorial one-liner shown next to the
// link. Title lookup happens at render time so the link text always matches
// the latest dossier title.
type relatedRef struct {
	Slug  string
	Gloss string
}

// engineLine matches "- **name** · layer · role · tier" (three middots, ✓/~ tier).
// Layer admits multi-token values like "reader-engine layer" / "character-engine layer"
// alongside the canonical "content" / "consumption" — finer taxonomies that emerged
// in later slot-tests should not be silently dropped by the parser.
var engineLine = regexp.MustCompile(`(?m)^-\s+\*\*(.+?)\*\*\s+·\s+([^·]+?)\s+·\s+(.+?)\s+·\s+(✓|~)`)

// mdOLLine matches an ordered-list-item line: "1. ", "12. ", etc. We strip
// the leading "N. " and treat the rest as the list-item content. mdBlock
// turns runs of these into a single <ol> block.
var mdOLLine = regexp.MustCompile(`^\d+\.\s+`)

// mdTableSeparator matches the GFM table separator row: `|----|----|...`
// (dashes, optional colons for alignment, pipes). Three dashes is the GFM
// minimum but two-dash and one-dash variants appear in the catalog too, so
// we accept any positive run of dashes per cell.
var mdTableSeparator = regexp.MustCompile(`^\|[\s:|-]*-+[\s:|-]*\|?\s*$`)

// --- a tiny markdown subset (no dependency) ---

var (
	mdCode  = regexp.MustCompile("`([^`]+?)`")
	mdLink  = regexp.MustCompile(`\[(.+?)\]\(([^)]+?)\)`)
	mdBold  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdItalU = regexp.MustCompile(`(^|[^\w])_([^_\n]+?)_([^\w]|$)`) // _italic_ (Gutenberg style), skipping snake_case
	mdItalS = regexp.MustCompile(`\*(.+?)\*`)
)

// mdInline escapes text, then renders code / links / bold / italic. Escaping first
// guarantees the only tags in the result are the ones injected here.
// mdSplitTableCells splits a GFM table row on unescaped pipes, trimming each
// cell's whitespace. Leading + trailing pipes are dropped; empty leading/
// trailing fields they would produce are stripped.
func mdSplitTableCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

func mdInline(s string) template.HTML {
	e := template.HTMLEscapeString(s)
	e = mdCode.ReplaceAllString(e, "<code>$1</code>")
	e = mdLink.ReplaceAllString(e, `<a href="$2">$1</a>`)
	e = mdBold.ReplaceAllString(e, "<b>$1</b>") // bold before italic so ** isn't eaten by *
	e = mdItalU.ReplaceAllString(e, "$1<i>$2</i>$3")
	e = mdItalS.ReplaceAllString(e, "<i>$1</i>")
	return template.HTML(e)
}

// mdBlock renders the block structure the dossiers/reviews actually use: ATX
// headings, > blockquotes (the verbatim quotes), - lists, --- rules, paragraphs.
func mdBlock(md string) template.HTML {
	var out strings.Builder
	lines := strings.Split(md, "\n")
	var para []string
	flush := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + string(mdInline(strings.Join(para, " "))) + "</p>\n")
			para = para[:0]
		}
	}
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case t == "":
			flush()
		case t == "---":
			flush()
			out.WriteString("<hr>\n")
		case strings.HasPrefix(t, "### "):
			flush()
			out.WriteString("<h3>" + string(mdInline(t[4:])) + "</h3>\n")
		case strings.HasPrefix(t, "## "):
			flush()
			out.WriteString("<h2>" + string(mdInline(t[3:])) + "</h2>\n")
		case strings.HasPrefix(t, "# "):
			flush()
			out.WriteString("<h1>" + string(mdInline(t[2:])) + "</h1>\n")
		case strings.HasPrefix(t, ">"):
			flush()
			var q []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
				i++
			}
			i--
			out.WriteString("<blockquote>" + string(mdInline(strings.Join(q, " "))) + "</blockquote>\n")
		case strings.HasPrefix(t, "- "):
			flush()
			out.WriteString("<ul>\n")
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "- ") {
				out.WriteString("<li>" + string(mdInline(strings.TrimSpace(lines[i])[2:])) + "</li>\n")
				i++
			}
			i--
			out.WriteString("</ul>\n")
		case mdOLLine.MatchString(t):
			flush()
			out.WriteString("<ol>\n")
			for i < len(lines) && mdOLLine.MatchString(strings.TrimSpace(lines[i])) {
				stripped := mdOLLine.ReplaceAllString(strings.TrimSpace(lines[i]), "")
				out.WriteString("<li>" + string(mdInline(stripped)) + "</li>\n")
				i++
			}
			i--
			out.WriteString("</ol>\n")
		case strings.HasPrefix(t, "|") && i+1 < len(lines) && mdTableSeparator.MatchString(strings.TrimSpace(lines[i+1])):
			flush()
			// GFM pipe table: header row, separator row, body rows. We've
			// verified the next line is the separator; consume the table.
			header := mdSplitTableCells(t)
			out.WriteString("<table>\n<thead><tr>")
			for _, c := range header {
				out.WriteString("<th>" + string(mdInline(c)) + "</th>")
			}
			out.WriteString("</tr></thead>\n<tbody>\n")
			i += 2 // skip header + separator; loop reads body rows
			for i < len(lines) {
				row := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(row, "|") {
					break
				}
				cells := mdSplitTableCells(row)
				out.WriteString("<tr>")
				for _, c := range cells {
					out.WriteString("<td>" + string(mdInline(c)) + "</td>")
				}
				out.WriteString("</tr>\n")
				i++
			}
			i--
			out.WriteString("</tbody>\n</table>\n")
		default:
			para = append(para, t)
		}
	}
	flush()
	return template.HTML(out.String())
}

// WorkFrontMatter is the typed YAML schema for works/*.md front-matter.
// This struct IS the schema-of-record: adding a field here documents it; a
// strict (KnownFields(true)) decode rejects any front-matter key that's not
// declared here, so typos like `bakcing:` surface as a parse error at db-sync
// time instead of silently empty downstream. The free-form `engine_status`,
// `queued_engines`, and `status` keys are accepted but unused (they carry
// authoring notes between sessions; the strict decoder needs them declared).
type WorkFrontMatter struct {
	Work          string   `yaml:"work"`
	Author        string   `yaml:"author"`
	Translator    string   `yaml:"translator"` // optional credit for translated editions (e.g. Hapgood 1888 for Notre-Dame de Paris)
	Year          string   `yaml:"year"`
	Medium        string   `yaml:"medium"`
	Backing       string   `yaml:"backing"`
	Source        string   `yaml:"source"`
	Layer         string   `yaml:"layer"`
	AuthorNote    string   `yaml:"author_note"`
	Engines       []string `yaml:"engines"`
	Verified      *bool    `yaml:"verified"`       // pointer — absent ≠ false; used by db-sync to write NULL vs 0
	RelatedWorks  []string `yaml:"related_works"`  // "slug :: gloss" lines; parseRelatedLine splits
	RelatedTheory []string `yaml:"related_theory"` // same
	PendingRefs   []string `yaml:"pending_refs"`   // same
	EngineStatus  string   `yaml:"engine_status"`  // authoring-note field, unused by tooling
	QueuedEngines []string `yaml:"queued_engines"` // authoring-note field, unused by tooling
	Status        string   `yaml:"status"`         // authoring-note field, unused by tooling
}

// parseRelatedLine splits one "slug :: gloss" line (the bullet form used in
// related_works / related_theory / pending_refs) into a typed relatedRef.
// Returns ok=false if the line doesn't contain the " :: " separator.
func parseRelatedLine(s string) (relatedRef, bool) {
	i := strings.Index(s, " :: ")
	if i < 0 {
		return relatedRef{}, false
	}
	return relatedRef{
		Slug:  strings.TrimSpace(s[:i]),
		Gloss: strings.TrimSpace(s[i+len(" :: "):]),
	}, true
}

func toRelatedRefs(lines []string) []relatedRef {
	out := make([]relatedRef, 0, len(lines))
	for _, ln := range lines {
		if ref, ok := parseRelatedLine(ln); ok {
			out = append(out, ref)
		}
	}
	return out
}

// splitFrontMatter parses the YAML front-matter (between leading and trailing
// `---` fences) into a typed WorkFrontMatter and returns the body after the
// closing fence. Returns an error if (a) the front-matter is unterminated or
// (b) yaml.v3 strict decoding fails — including unknown keys (typo defense).
// Files with no front-matter return a zero-value WorkFrontMatter and nil error.
func splitFrontMatter(text string) (WorkFrontMatter, string, error) {
	var fm WorkFrontMatter
	if !strings.HasPrefix(text, "---") {
		return fm, text, nil
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return fm, text, fmt.Errorf("unterminated front-matter (no closing `---`)")
	}
	block := text[3 : 3+end]
	body := text[3+end+4:]
	dec := yaml.NewDecoder(strings.NewReader(block))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return fm, body, err
	}
	return fm, body, nil
}

func parseCard(text string) workCard {
	fm, body, _ := splitFrontMatter(text)
	reading, evidencePresent := splitWorkBody(body)
	c := workCard{
		Work: fm.Work, Author: fm.Author, Year: fm.Year,
		Medium: fm.Medium, Backing: fm.Backing, Source: fm.Source,
		Translator:    fm.Translator,
		AuthorNote:    fm.AuthorNote,
		Bead:          sectionText(body, "**The bead.**"),
		ReadingBody:   mdBlock(reading),
		FullBody:      mdBlock(body),
		HasEvidence:   evidencePresent,
		RelatedWorks:  toRelatedRefs(fm.RelatedWorks),
		RelatedTheory: toRelatedRefs(fm.RelatedTheory),
		PendingRefs:   toRelatedRefs(fm.PendingRefs),
	}
	for _, mm := range engineLine.FindAllStringSubmatch(body, -1) {
		role := strings.Trim(mm[3], "*")
		c.Engines = append(c.Engines, engineTag{
			Name: mm[1], Role: role, Tier: mm[4],
			Excluded: strings.Contains(strings.ToLower(role), "exclud"),
		})
	}
	return c
}

// splitWorkBody returns the `## The reading` section's body (without the
// heading) and whether a `## The evidence` heading appears in the file.
func splitWorkBody(body string) (reading string, hasEvidence bool) {
	hasEvidence = strings.Contains(body, "## The evidence")
	if i := strings.Index(body, "## The reading"); i >= 0 {
		rest := body[i+len("## The reading"):]
		if j := strings.Index(rest, "\n## "); j >= 0 {
			return strings.TrimSpace(rest[:j]), hasEvidence
		}
		return strings.TrimSpace(rest), hasEvidence
	}
	return body, hasEvidence
}

// sectionText returns the inline text after a bold lead-in ("**The bead.**"),
// up to the paragraph break.
func sectionText(body, lead string) string {
	i := strings.Index(body, lead)
	if i < 0 {
		return ""
	}
	rest := body[i+len(lead):]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(strings.ReplaceAll(rest, "\n", " "))
}

func loadWorks(dir string) []workCard {
	var cards []workCard
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
		c := parseCard(strings.ReplaceAll(string(b), "\r", ""))
		c.File = base
		c.Slug = strings.TrimSuffix(base, ".md")
		if c.Work != "" {
			cards = append(cards, c)
		}
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Work < cards[j].Work })
	return cards
}

// --- engines: parse the README "## The engines" section ---

type workRef struct{ Work, Href string }

type engineSpec struct {
	Name, Tagline, Slug string
	Body                template.HTML // the full ### section (slots + guard + specimens + counterfeit)
	Works               []workRef     // works tagged with this engine
}

// slugify lowercases and collapses non-alphanumerics to single dashes.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// loadEngines reads the canonical engine spec from the README's "## The engines"
// section. Each "### Name — tagline" block becomes one engineSpec. The README
// is the single source of truth, so the spec never drifts.
func loadEngines(readmePath string, cards []workCard) []engineSpec {
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		return nil
	}
	text := strings.ReplaceAll(string(raw), "\r", "")
	start := strings.Index(text, "## The engines")
	if start < 0 {
		return nil
	}
	sect := text[start:]
	if e := strings.Index(sect, "\nEngines compose."); e >= 0 { // the list ends here
		sect = sect[:e]
	} else if e := strings.Index(sect[len("## The engines"):], "\n## "); e >= 0 {
		sect = sect[:e+len("## The engines")]
	}

	worksByEngine := map[string][]workRef{}
	for _, c := range cards {
		href := "/cupel/works/" + c.Slug + "/"
		seen := map[string]bool{}
		for _, e := range c.Engines {
			key := strings.ToLower(e.Name)
			if e.Excluded || seen[key] {
				continue
			}
			seen[key] = true
			worksByEngine[key] = append(worksByEngine[key], workRef{Work: c.Work, Href: href})
		}
	}

	// The "## The engines" section is a markdown table — one row per engine,
	// `| [name](…/engines/<slug>/) | counterfeit |`, ordered by tag frequency.
	// (It used to be `### Name — tagline` subsections; the table rewrite for the
	// public README silently emptied this parser, blanking the /engines/ list.)
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	var specs []engineSpec
	for _, line := range strings.Split(sect, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		// Split the row into trimmed cells, dropping the empties the leading
		// and trailing pipes produce.
		var cells []string
		for _, c := range strings.Split(strings.Trim(line, "|"), "|") {
			cells = append(cells, strings.TrimSpace(c))
		}
		if len(cells) < 2 {
			continue
		}
		// Only data rows carry the engine link in column 1; the header and the
		// `|---|` separator have none, so they fall through.
		m := linkRe.FindStringSubmatch(cells[0])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		slug := engineSlugFromURL(m[2])
		if slug == "" {
			slug = slugify(name)
		}
		// The compact table carries no separate wish-tagline, so column 2 — the
		// counterfeit, "the grift wearing its face" — becomes the engine's
		// list/detail subtitle.
		specs = append(specs, engineSpec{
			Name: name, Tagline: cells[1], Slug: slug,
			Works: worksByEngine[strings.ToLower(name)],
		})
	}
	return specs
}

// engineSlugFromURL pulls <slug> out of a `…/engines/<slug>/` link so the
// generated pages match the README's canonical engine URLs exactly.
func engineSlugFromURL(u string) string {
	u = strings.Trim(strings.TrimSpace(u), "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return ""
}

// --- linkify: post-process rendered HTML to clean up cross-refs ---

// esc is a thin wrapper around html/template's escaper so callers don't import
// the package directly.
func esc(s string) string { return template.HTMLEscapeString(s) }

// codeWorksRef catches `<code>works/<slug>.md</code>` inline references that
// landed in rendered HTML from the source markdown. We turn each into a real
// link to the work, displaying the title in italics (the typographic convention
// for book/film titles).
var codeWorksRef = regexp.MustCompile(`<code>works/([a-z0-9-]+)\.md</code>`)

// wikilinkWorksRef catches the Obsidian-style [[slug]] cross-refs that recent
// dossiers use for sibling-specimen lists. Markdown rendering passes them
// through unchanged, so we match the literal pattern in the rendered HTML and
// resolve to the same italicized link the codeWorksRef pass produces.
var wikilinkWorksRef = regexp.MustCompile(`\[\[([a-z0-9-]+)\]\]`)

// codeTheoryRef catches `<code>theory/<slug>.md</code>` — these survived the
// 2026-06-07 dossier scrub because README.md still carries them in the engine
// spec sections. Render handler converts to a live link when the slug is a
// known public theory doc; strips to empty otherwise (matching the unknown
// works/ branch).
var codeTheoryRef = regexp.MustCompile(`<code>theory/([a-z0-9-]+)\.md</code>`)

// codeOtherPrivateRef matches the gitignored / non-public references that
// remain in body content (NOTES.md, cmd/foo.go, README.md self-refs,
// theory/working/X.md). The renderer strips them to empty so the public site
// doesn't show raw filesystem paths in prose.
var codeOtherPrivateRef = regexp.MustCompile(`<code>(NOTES\.md|cmd/[a-z0-9/_.]+\.(?:go|json)|theory/working/[a-z0-9-]+\.md|README\.md)</code>`)

// parenWithStrippedRef catches "(...stripped to whitespace...)" — a
// parenthetical whose content collapsed to whitespace after the strips above.
// Just drop the whole "()" so the surrounding prose reads cleanly.
var parenWithStrippedRef = regexp.MustCompile(`\s*\(\s*\)`)

// linkifyTarget is one term-to-href mapping the cluster/glossary linker can
// install. Patterns are compiled once and re-used per body. The `used` flag
// is a per-pass flag — first occurrence only — so dossier prose isn't carpeted
// with the same link repeated paragraph after paragraph.
type linkifyTarget struct {
	pattern *regexp.Regexp
	href    string
	used    bool
}

// skipInsideTags is the set of HTML elements where the cluster/glossary
// linker MUST NOT touch the inner text:
//
//   - a — already a link; nesting links is invalid HTML
//   - code — code refs, slugs, file paths; semantic markup, not prose
//   - b — the bold leadins in dossiers (`**spine** · ...`) and engine names
//   - i — italicized titles (*Watchmen*) and engine names (*mastery*)
//   - blockquote — verbatim primary-text quotes; never modify
//   - h1-h3 — headings (the "## The reading" / "### Subhead" form)
var skipInsideTags = map[string]bool{
	"a": true, "code": true, "b": true, "i": true,
	"blockquote": true, "h1": true, "h2": true, "h3": true,
}

// tagOpenClose splits the body into tokens at `<` and `>` boundaries so the
// linker can track which tags wrap each text region. RE2 anchors at the
// boundary characters; the alternation keeps the open form and close form
// distinguishable from the captured tag name.
var tagOpenClose = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)

// linkifyClustersAndGlossary walks the body, identifies text regions outside
// the skipInsideTags set, and runs first-occurrence replacement against the
// supplied targets (cluster forms, then glossary aliases). Targets are tried
// in the order given — caller pre-sorts so longer aliases run first.
//
// The walk is single-pass and side-effecting: each target's `used` flag flips
// the first time its pattern fires inside a safe text region, suppressing
// subsequent matches in the same body. This keeps a card's prose from being
// carpeted with the same link N times.
func linkifyClustersAndGlossary(body string, targets []linkifyTarget) string {
	var out strings.Builder
	var stack []string
	pos := 0
	for _, m := range tagOpenClose.FindAllStringSubmatchIndex(body, -1) {
		// Text region before this tag.
		text := body[pos:m[0]]
		out.WriteString(applyLinkTargets(text, targets, !stackHasSkip(stack)))
		// The tag itself — written unchanged.
		out.WriteString(body[m[0]:m[1]])
		isClose := body[m[2]:m[3]] == "/"
		name := strings.ToLower(body[m[4]:m[5]])
		if isClose {
			// Pop the matching name if found near the top of the stack.
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == name {
					stack = append(stack[:i], stack[i+1:]...)
					break
				}
			}
		} else if !isVoidTag(body[m[0]:m[1]]) {
			stack = append(stack, name)
		}
		pos = m[1]
	}
	if pos < len(body) {
		out.WriteString(applyLinkTargets(body[pos:], targets, !stackHasSkip(stack)))
	}
	return out.String()
}

func stackHasSkip(stack []string) bool {
	for _, t := range stack {
		if skipInsideTags[t] {
			return true
		}
	}
	return false
}

// isVoidTag detects self-closing tags (`<br/>`, `<hr>`, `<img src=…>`) so the
// walk doesn't push them onto the open-tag stack.
func isVoidTag(tag string) bool {
	if strings.HasSuffix(tag, "/>") {
		return true
	}
	low := strings.ToLower(tag)
	for _, v := range []string{"<br", "<hr", "<img", "<input", "<meta", "<link "} {
		if strings.HasPrefix(low, v) && (len(low) == len(v) || low[len(v)] == ' ' || low[len(v)] == '>') {
			return true
		}
	}
	return false
}

// applyLinkTargets walks the text left-to-right, repeatedly finding the
// earliest match of any unused target and emitting the wrapped link inline.
// Critically it advances past the injected `<a>` so subsequent targets can
// never match inside an href attribute — the bug the prior in-place rewrite
// hit when one term's slug embedded another term as a substring (e.g.
// `consumption-layer` inside `/glossary/#content-layer-consumption-layer`).
//
// When `enabled` is false (text sits inside a skip-tag like <code> or
// <blockquote>), the text passes through unchanged.
func applyLinkTargets(text string, targets []linkifyTarget, enabled bool) string {
	if !enabled || text == "" {
		return text
	}
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		bestStart, bestEnd, bestIdx := -1, -1, -1
		for i := range targets {
			if targets[i].used {
				continue
			}
			loc := targets[i].pattern.FindStringIndex(text[pos:])
			if loc == nil {
				continue
			}
			start := pos + loc[0]
			// Earliest match wins; on a tie, the target with the longer
			// matched span wins so prefix-aliases lose to fuller forms.
			if bestIdx < 0 || start < bestStart || (start == bestStart && (pos+loc[1]) > bestEnd) {
				bestStart, bestEnd, bestIdx = start, pos+loc[1], i
			}
		}
		if bestIdx < 0 {
			out.WriteString(text[pos:])
			return out.String()
		}
		out.WriteString(text[pos:bestStart])
		out.WriteString(fmt.Sprintf(`<a href="%s">%s</a>`, targets[bestIdx].href, text[bestStart:bestEnd]))
		targets[bestIdx].used = true
		pos = bestEnd
	}
	return out.String()
}

// freshTargets clones a target list with all `used` flags reset, so each
// body starts with first-occurrence tracking from scratch. Sharing the
// compiled regex pointers across copies keeps the per-card cost ~O(N).
func freshTargets(canonical []linkifyTarget) []linkifyTarget {
	out := make([]linkifyTarget, len(canonical))
	for i, t := range canonical {
		out[i] = linkifyTarget{pattern: t.pattern, href: t.href}
	}
	return out
}

// buildClusterAndGlossaryTargets compiles the linkifyTarget list for one
// rendering pass. Cluster patterns come first (their referent — `the X
// cluster` — is unambiguous); glossary aliases are sorted longest-first so
// `cluster-internal participant-refusal` matches before any shorter alias
// that happens to be its prefix.
//
// Glossary aliases are filtered to multi-word or hyphenated forms of at
// least 10 characters. The cutoff suppresses the high-traffic single-word
// terms ("engine", "wound", "leg", "pole", "mode", "register") that would
// otherwise carpet every card with links and bury the actual cross-refs.
func buildClusterAndGlossaryTargets(clusters []clusterSpec, glossary []glossaryEntry, linkPrefix string) []linkifyTarget {
	var targets []linkifyTarget
	for _, c := range clusters {
		// Match `the <slug> cluster` case-insensitively. The slug is the
		// catalog's stable identifier; dossier prose was converted to this
		// form in the cluster name-stability refactor.
		pat := regexp.MustCompile(`(?i)\bthe ` + regexp.QuoteMeta(c.Slug) + ` cluster\b`)
		targets = append(targets, linkifyTarget{
			pattern: pat,
			href:    linkPrefix + "clusters/" + c.Slug + "/",
		})
	}
	type aliasRef struct {
		text string
		slug string
	}
	var aliases []aliasRef
	for _, e := range glossary {
		// The linkable allow-list (theory/glossary-linkable.txt) is the
		// promotion gate. An entry without Linkable=true gets a /glossary/#
		// anchor page but is NEVER auto-linked from dossier prose. Default
		// is empty — promotion is deliberate, per-slug.
		if !e.Linkable {
			continue
		}
		forms := e.Aliases
		if len(forms) == 0 {
			forms = []string{e.Term}
		}
		for _, f := range forms {
			if !shouldLinkifyGlossaryTerm(f) {
				continue
			}
			aliases = append(aliases, aliasRef{text: f, slug: e.Slug})
		}
	}
	// Longest alias first: prefix-matches lose to fuller forms.
	sort.Slice(aliases, func(i, j int) bool { return len(aliases[i].text) > len(aliases[j].text) })
	for _, a := range aliases {
		pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(a.text) + `\b`)
		targets = append(targets, linkifyTarget{
			pattern: pat,
			href:    linkPrefix + "glossary/#" + a.slug,
		})
	}
	return targets
}

// shouldLinkifyGlossaryTerm filters glossary terms to multi-word or
// hyphenated forms ≥ 10 characters. Short single-word entries
// ("engine", "wound", "leg", "pole", "register") appear too frequently in
// dossier prose to safely link without carpeting the page.
func shouldLinkifyGlossaryTerm(term string) bool {
	if len(term) < 10 {
		return false
	}
	if !strings.ContainsAny(term, " -") {
		return false
	}
	// "slot 1" / "slot 2" / "slot 3" — generic slot references appear in
	// every dossier and shouldn't all point at the same anchor.
	if strings.HasPrefix(term, "slot ") {
		return false
	}
	return true
}

// linkifyWorkRefs post-processes the rendered body HTML to clean up the
// code-span cross-refs that look ugly on a literary-analysis page. linkPrefix
// is what to prepend to the per-slug route — "/cupel/works/" for the deployed
// Astro site, which uses trailing-slash directory routes (works/<slug>/), not
// a `.html` suffix.
//
// Two ref shapes are converted to live links: `<code>works/SLUG.md</code>` to
// a work page (when SLUG is in workTitleBySlug), and `<code>theory/SLUG.md</code>`
// to a theory page (when SLUG is in theoryTitleBySlug). Unknown public refs
// strip to empty; private refs (NOTES.md, cmd/, theory/working/) also strip.
// Empty "()" left by the strips collapse via parenWithStrippedRef.
func linkifyWorkRefs(body string, workTitleBySlug, theoryTitleBySlug map[string]string, linkPrefix, theoryPrefix string) string {
	body = wikilinkWorksRef.ReplaceAllStringFunc(body, func(match string) string {
		mm := wikilinkWorksRef.FindStringSubmatch(match)
		slug := mm[1]
		title, ok := workTitleBySlug[slug]
		if !ok {
			// Not a works/ slug — could be a memory-system reference or
			// freeform note; preserve the original brackets rather than
			// silently strip them.
			return match
		}
		return fmt.Sprintf(`<a href="%s%s/"><i>%s</i></a>`, linkPrefix, slug, esc(title))
	})
	body = codeWorksRef.ReplaceAllStringFunc(body, func(match string) string {
		mm := codeWorksRef.FindStringSubmatch(match)
		slug := mm[1]
		title, ok := workTitleBySlug[slug]
		if !ok {
			// Unknown public-target ref — strip silently (the works-lint extension
			// blocks new ones at commit time; a stray here is benign).
			return ""
		}
		return fmt.Sprintf(`<a href="%s%s/"><i>%s</i></a>`, linkPrefix, slug, esc(title))
	})
	body = codeTheoryRef.ReplaceAllStringFunc(body, func(match string) string {
		mm := codeTheoryRef.FindStringSubmatch(match)
		slug := mm[1]
		title, ok := theoryTitleBySlug[slug]
		if !ok {
			return ""
		}
		return fmt.Sprintf(`<a href="%s%s/">%s</a>`, theoryPrefix, slug, esc(title))
	})
	body = codeOtherPrivateRef.ReplaceAllString(body, "")
	body = parenWithStrippedRef.ReplaceAllString(body, "")
	return body
}

// italicTitleInProse matches `<i>...</i>` runs in hand-curated prose (cluster
// pages). The body in question — cluster specimens_html — uses italics
// consistently for work titles, so a uniqueness-checked match-to-slug pass is
// safe here; the same pass is NOT applied to dossier bodies, where italics
// also carry engine names and emphasis.
var italicTitleInProse = regexp.MustCompile(`<i>([^<]+)</i>`)

// normalizeTitleForLookup reduces a work title to a canonical match key:
// lowercase, leading article stripped, trailing parenthetical stripped,
// subtitle after ": " stripped, whitespace around "/" collapsed. Used both
// when building the work-title index and when looking up cluster prose
// italics against it.
//
// The colon-subtitle strip lets cluster-prose `*Dianetics*` match the
// dossier title "Dianetics: The Modern Science of Mental Health". The
// slash whitespace normalization lets `*Peoples Temple/Jonestown*` match
// "Peoples Temple / Jonestown (…)". If two distinct dossiers normalize to
// the same key the linker drops both — uniqueness keeps it conservative.
func normalizeTitleForLookup(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			break
		}
	}
	if i := strings.LastIndex(s, "("); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	if i := strings.Index(s, ": "); i > 0 {
		s = s[:i]
	}
	// Collapse whitespace around "/" so "x / y" and "x/y" share a key.
	s = strings.ReplaceAll(s, " / ", "/")
	s = strings.ReplaceAll(s, " /", "/")
	s = strings.ReplaceAll(s, "/ ", "/")
	return s
}

// linkifyItalicWorkTitles wraps each `<i>title</i>` in the body with a link
// to the matching work page. Resolution order: exact normalized-title match
// first, then (with allowPrefix=true) a uniqueness-checked prefix match so
// short cluster-prose forms like "7 Habits" resolve to "The 7 Habits of
// Highly Effective People". Dossier bodies pass allowPrefix=false because
// italics there are used freely for emphasis ("go *back*" must not
// prefix-match to "Back to the Future") — only exact normalized matches link.
func linkifyItalicWorkTitles(body string, titleToSlug map[string]string, linkPrefix string, allowPrefix bool) string {
	return italicTitleInProse.ReplaceAllStringFunc(body, func(match string) string {
		m := italicTitleInProse.FindStringSubmatch(match)
		key := normalizeTitleForLookup(m[1])
		if slug, ok := titleToSlug[key]; ok {
			return fmt.Sprintf(`<a href="%s%s/">%s</a>`, linkPrefix, slug, match)
		}
		if !allowPrefix {
			return match
		}
		// Fallback: unique prefix match. The prose form "7 Habits" prefixes
		// only "7 habits of highly effective people", so it links; "secret"
		// would prefix both "secret" and "secret garden", so it skips.
		var hit string
		count := 0
		for k, slug := range titleToSlug {
			if strings.HasPrefix(k, key+" ") {
				hit = slug
				count++
				if count > 1 {
					break
				}
			}
		}
		if count == 1 {
			return fmt.Sprintf(`<a href="%s%s/">%s</a>`, linkPrefix, hit, match)
		}
		return match
	})
}
