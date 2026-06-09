package main

// glossary.go — parse theory/glossary.md into the per-term JSON the Astro
// /glossary/ page renders. Each entry is a single bold-leadin paragraph; the
// leadin's bracketed term becomes the entry's anchor slug. Paired forms
// (`**X ↔ Y.**`, `**X vs Y.**`) split into aliases so the linkifier matches
// either half — the canonical entry stays paired.

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type glossaryEntry struct {
	Section        string   `json:"section"`
	Term           string   `json:"term"`
	Aliases        []string `json:"aliases,omitempty"`
	Slug           string   `json:"slug"`
	DefinitionHTML string   `json:"definition_html"`
	// Linkable: true means the auto-linker in parse.go may rewrite
	// occurrences of this term (and its aliases) in dossier/cluster/engine
	// bodies into <a> tags pointing at /glossary/#slug. Defaults to false
	// — promotion is a deliberate act recorded in theory/glossary-linkable.txt.
	Linkable bool `json:"linkable,omitempty"`
}

// loadLinkableSlugs reads the per-line allow-list from
// theory/glossary-linkable.txt. Missing file => empty set (zero terms
// auto-link). Lines starting with `#` are comments; blank lines ignored.
func loadLinkableSlugs(path string) map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out[t] = true
	}
	return out
}

// glossaryLeadin matches the entry-leadin form `**<term>[.]**` at the start of
// a paragraph. `<term>` must not contain `:` (filters out label paragraphs like
// `**The discipline:**`) or contain a backtick (filters out code-decorated
// non-entries). Period-after-term is optional — `**wound** is …` is a real
// entry shape used in the foundation section.
var glossaryLeadin = regexp.MustCompile(`^\*\*([^*:` + "`" + `]+?)\*\*`)

func loadGlossary(path string) ([]glossaryEntry, error) {
	return loadGlossaryWithLinkable(path, nil)
}

func loadGlossaryWithLinkable(path string, linkable map[string]bool) ([]glossaryEntry, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("glossary: open %s: %w", path, err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("glossary: scan %s: %w", path, err)
	}

	var entries []glossaryEntry
	section := ""
	inSection := false

	// Walk paragraphs. A paragraph is a block of consecutive non-blank lines.
	var para []string
	flushPara := func() {
		if !inSection || len(para) == 0 {
			para = para[:0]
			return
		}
		text := strings.Join(para, "\n")
		para = para[:0]
		mm := glossaryLeadin.FindStringSubmatch(strings.TrimSpace(text))
		if mm == nil {
			return
		}
		rawTerm := strings.TrimSpace(mm[1])
		canonical := strings.TrimRight(rawTerm, ".")
		canonical = strings.TrimSpace(canonical)
		if canonical == "" {
			return
		}
		// Split paired forms into aliases. The canonical term keeps the pair;
		// aliases get each half (with parenthetical strip), since dossier prose
		// references the halves individually ("the consumption-layer", not the
		// pair).
		aliases := splitGlossaryAliases(canonical)
		slug := slugify(canonical)
		entries = append(entries, glossaryEntry{
			Section:        section,
			Term:           canonical,
			Aliases:        aliases,
			Slug:           slug,
			DefinitionHTML: string(mdBlock(text)),
			Linkable:       linkable[slug],
		})
	}

	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		// Section heading: `## N. Title`. Subsection `### Title` updates the
		// section's display string but doesn't reset the entry stream — entries
		// under subsections still belong to the parent section for grouping.
		if strings.HasPrefix(trim, "## ") {
			flushPara()
			section = strings.TrimPrefix(trim, "## ")
			// Skip the preamble section ("Glossary — cupel vocabulary, …" was
			// `# ` so it never lands here; the first `## ` is `## 1. Foundation`).
			if strings.HasPrefix(section, "What this glossary deliberately does not contain") {
				// Pruned-terms section — descriptive, not entries.
				inSection = false
				continue
			}
			inSection = true
			continue
		}
		if strings.HasPrefix(trim, "### ") {
			flushPara()
			continue
		}
		if trim == "" {
			flushPara()
			continue
		}
		if strings.HasPrefix(trim, "---") {
			flushPara()
			continue
		}
		para = append(para, ln)
	}
	flushPara()

	return entries, nil
}

// splitGlossaryAliases extracts the individual halves of paired-form terms
// (`X ↔ Y`, `X vs Y`) so the linkifier can match either half independently.
// Returns nil when the term has no recognizable split point.
func splitGlossaryAliases(canonical string) []string {
	for _, sep := range []string{" ↔ ", " vs ", " (vs "} {
		if strings.Contains(canonical, sep) {
			parts := strings.Split(canonical, sep)
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				p = strings.TrimRight(p, ")")
				p = strings.TrimSpace(p)
				if p != "" && p != canonical {
					out = append(out, p)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}
