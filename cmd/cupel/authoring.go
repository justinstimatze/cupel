package main

// authoring.go — write-side CLIs that mutate works/*.md front-matter while
// keeping the file format on-disk stable (no yaml.v3 round-trip churn). The
// invariant: each command produces the minimum surgical text edit so diffs
// stay readable. After every mutation, the author runs `cupel db-sync` +
// `cupel works-lint` to gate the change — same surface that pre-commit uses.
//
//   cupel new-work       scaffold a new works/<slug>.md with valid front-matter shape
//   cupel related-add    add a related_works / related_theory / pending_refs bullet
//   cupel related-rm     remove a bullet whose target matches
//   cupel work-set       update a top-level scalar field (backing, source, layer, ...)
//
// These four collapse the daily authoring workflow: previously every new
// dossier meant hand-quoting YAML values that contain `: ` and quoting
// every `slug :: gloss` bullet. With these commands, the quoting rules are
// enforced at write time so typos never reach the strict-decode gate.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// quoteValueIfNeeded applies the same quoting rules as migrate-fm-quotes:
// wrap a scalar value in double quotes (with `\` and `"` escaping) when it
// contains `: ` (the YAML mapping operator). Empty/already-quoted/flow-style
// values are left alone.
func quoteValueIfNeeded(val string) string {
	if !needsScalarQuoting(val) {
		return val
	}
	return yamlQuote(val)
}

// quoteBulletIfNeeded forces quoting on every `slug :: gloss` bullet (since
// real YAML otherwise parses `slug :: gloss` as `{slug: ": gloss"}` — a map,
// not a string).
func quoteBulletIfNeeded(val string) string {
	if !needsBulletQuoting(val) {
		return val
	}
	return yamlQuote(val)
}

func runNewWork(args []string) {
	fs := flag.NewFlagSet("new-work", flag.ExitOnError)
	slug := fs.String("slug", "", "kebab-case slug (becomes works/<slug>.md)")
	work := fs.String("work", "", "work title (required)")
	author := fs.String("author", "", "author (required)")
	translator := fs.String("translator", "", "translator credit (optional)")
	year := fs.String("year", "", "year string — flexible: '1813', '2009, 2012, 2020', '~399 BCE'")
	medium := fs.String("medium", "novel", "novel | film | TV | nonfiction | ...")
	backing := fs.String("backing", "reviewed", "reviewed | slot-proven")
	source := fs.String("source", "", "citation string (e.g. Project Gutenberg #1342, ...)")
	layer := fs.String("layer", "content", "content | consumption | reader-engine layer | ...")
	engines := fs.String("engines", "", "comma-separated engine names (e.g. 'repricing,mastery')")
	authorNote := fs.String("author-note", "", "optional ⚠ badge note for author-conduct concerns")
	_ = fs.Parse(args)

	if *slug == "" || *work == "" || *author == "" {
		fmt.Fprintln(os.Stderr, "new-work: --slug, --work, --author required")
		os.Exit(2)
	}
	if !validSlug(*slug) {
		fmt.Fprintf(os.Stderr, "new-work: --slug must match [a-z0-9-]+ (got %q)\n", *slug)
		os.Exit(2)
	}
	path := filepath.Join("works", *slug+".md")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "new-work: %s already exists\n", path)
		os.Exit(1)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("work: " + quoteValueIfNeeded(*work) + "\n")
	b.WriteString("author: " + quoteValueIfNeeded(*author) + "\n")
	if *translator != "" {
		b.WriteString("translator: " + quoteValueIfNeeded(*translator) + "\n")
	}
	if *year != "" {
		b.WriteString("year: " + quoteValueIfNeeded(*year) + "\n")
	}
	b.WriteString("medium: " + quoteValueIfNeeded(*medium) + "\n")
	b.WriteString("backing: " + *backing + "\n")
	if *source != "" {
		b.WriteString("source: " + quoteValueIfNeeded(*source) + "\n")
	}
	enginesList := splitCSV(*engines)
	b.WriteString("engines: [" + strings.Join(enginesList, ", ") + "]\n")
	b.WriteString("layer: " + quoteValueIfNeeded(*layer) + "\n")
	b.WriteString("verified: false\n")
	if *authorNote != "" {
		b.WriteString("author_note: " + quoteValueIfNeeded(*authorNote) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("## The reading\n\n")
	b.WriteString("**The bead.** \n\n")
	b.WriteString("**Engines**\n\n")
	for _, eng := range enginesList {
		b.WriteString("- **" + eng + "** · " + *layer + " · spine · ~ — \n")
	}
	if len(enginesList) == 0 {
		b.WriteString("- **<engine>** · " + *layer + " · spine · ~ — \n")
	}
	b.WriteString("\n**Verdict.** \n")
	if *backing == "slot-proven" {
		b.WriteString("\n## The evidence\n\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "new-work: write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "new-work: created %s — fill the bead + engine prose, then `cupel db-sync && cupel works-lint`\n", path)
}

func runRelatedAdd(args []string) {
	fs := flag.NewFlagSet("related-add", flag.ExitOnError)
	from := fs.String("slug", "", "FROM dossier slug")
	to := fs.String("to", "", "TARGET slug")
	kind := fs.String("kind", "work", "work | theory | pending")
	gloss := fs.String("gloss", "", "editorial gloss")
	_ = fs.Parse(args)

	if *from == "" || *to == "" || *gloss == "" {
		fmt.Fprintln(os.Stderr, "related-add: --slug, --to, --gloss required")
		os.Exit(2)
	}
	listKey, err := relatedKindKey(*kind)
	if err != nil {
		fmt.Fprintln(os.Stderr, "related-add:", err)
		os.Exit(2)
	}
	path := filepath.Join("works", *from+".md")
	text, err := readFileNorm(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "related-add:", err)
		os.Exit(1)
	}

	bullet := *to + " :: " + *gloss
	newText, added, err := addBulletToFrontMatter(text, listKey, bullet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "related-add:", err)
		os.Exit(1)
	}
	if !added {
		fmt.Fprintf(os.Stderr, "related-add: %s -> %s already present in %s\n", *from, *to, listKey)
		return
	}
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "related-add: write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "related-add: %s/%s -> %s :: %s\n", path, listKey, *to, *gloss)
}

func runRelatedRm(args []string) {
	fs := flag.NewFlagSet("related-rm", flag.ExitOnError)
	from := fs.String("slug", "", "FROM dossier slug")
	to := fs.String("to", "", "TARGET slug to remove")
	_ = fs.Parse(args)

	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "related-rm: --slug, --to required")
		os.Exit(2)
	}
	path := filepath.Join("works", *from+".md")
	text, err := readFileNorm(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "related-rm:", err)
		os.Exit(1)
	}
	newText, removed := removeBulletFromFrontMatter(text, *to)
	if !removed {
		fmt.Fprintf(os.Stderr, "related-rm: no bullet targeting %s found in %s\n", *to, path)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "related-rm: write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "related-rm: removed bullet %s -> %s\n", *from, *to)
}

func runWorkSet(args []string) {
	fs := flag.NewFlagSet("work-set", flag.ExitOnError)
	slug := fs.String("slug", "", "dossier slug")
	field := fs.String("field", "", "field name (e.g. backing, source, translator, layer, verified)")
	value := fs.String("value", "", "new value (use --value '' to clear)")
	_ = fs.Parse(args)

	if *slug == "" || *field == "" {
		fmt.Fprintln(os.Stderr, "work-set: --slug, --field required")
		os.Exit(2)
	}
	if !isKnownScalarField(*field) {
		fmt.Fprintf(os.Stderr, "work-set: --field must be one of %v (got %q)\n", knownScalarFields, *field)
		os.Exit(2)
	}
	path := filepath.Join("works", *slug+".md")
	text, err := readFileNorm(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "work-set:", err)
		os.Exit(1)
	}
	newText, updated, err := setScalarInFrontMatter(text, *field, *value)
	if err != nil {
		fmt.Fprintln(os.Stderr, "work-set:", err)
		os.Exit(1)
	}
	if !updated {
		fmt.Fprintf(os.Stderr, "work-set: %s in %s already equals %q (no-op)\n", *field, path, *value)
		return
	}
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "work-set: write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "work-set: %s/%s = %s\n", path, *field, *value)
}

// ----- helpers (small, focused, no external deps) -----

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	scalarKey    = regexp.MustCompile(`^([a-zA-Z_]+):\s*(.*)$`)
	frontFenceRE = regexp.MustCompile(`(?m)^---\s*$`)
)

// knownScalarFields names the WorkFrontMatter fields work-set can mutate.
// The list-form fields (related_works/related_theory/pending_refs) use the
// related-add / related-rm CLIs instead — they have different semantics.
var knownScalarFields = []string{
	"work", "author", "translator", "year", "medium", "backing",
	"source", "layer", "author_note", "verified",
	"engine_status", "status",
}

func isKnownScalarField(f string) bool {
	for _, k := range knownScalarFields {
		if k == f {
			return true
		}
	}
	return false
}

func validSlug(s string) bool {
	return slugPattern.MatchString(s)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func relatedKindKey(kind string) (string, error) {
	switch kind {
	case "work":
		return "related_works", nil
	case "theory":
		return "related_theory", nil
	case "pending":
		return "pending_refs", nil
	}
	return "", fmt.Errorf("--kind must be work | theory | pending (got %q)", kind)
}

func readFileNorm(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r", ""), nil
}

// frontMatterBounds returns (startIdxAfterOpenFence, endIdxAtCloseFence) — the
// indices delimiting the YAML block between the leading and trailing `---`
// fences. Returns (-1, -1) if the file has no front-matter.
func frontMatterBounds(text string) (int, int) {
	if !strings.HasPrefix(text, "---") {
		return -1, -1
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return -1, -1
	}
	return 3, 3 + end // [3:end] is the YAML block content
}

// addBulletToFrontMatter appends `  - "BULLET"` under the list-key in the
// front-matter. Inserts the list-key itself (with a leading blank line) if
// absent. Idempotent on exact-bullet match. Returns (newText, added, err).
func addBulletToFrontMatter(text, listKey, bullet string) (string, bool, error) {
	start, end := frontMatterBounds(text)
	if start < 0 {
		return text, false, fmt.Errorf("no front-matter in input")
	}
	block := text[start:end]
	body := text[end:]
	lines := strings.Split(block, "\n")

	quoted := quoteBulletIfNeeded(bullet)
	bulletLine := "  - " + quoted
	want := strings.TrimSpace(bullet) // for de-dup, compare on the unquoted form

	// Find existing list-key block.
	listStart := -1
	for i, ln := range lines {
		if mm := scalarKey.FindStringSubmatch(ln); mm != nil && mm[1] == listKey && strings.TrimSpace(mm[2]) == "" {
			listStart = i
			break
		}
	}
	if listStart >= 0 {
		// Check for an existing matching bullet inside this list-section.
		i := listStart + 1
		for i < len(lines) {
			s := strings.TrimRight(lines[i], " ")
			if !strings.HasPrefix(s, "  - ") {
				break
			}
			payload := strings.TrimSpace(strings.TrimPrefix(s, "  - "))
			payload = strings.TrimPrefix(strings.TrimSuffix(payload, `"`), `"`)
			if payload == want {
				return text, false, nil
			}
			i++
		}
		// Insert just before the next non-bullet line (i).
		newLines := append([]string{}, lines[:i]...)
		newLines = append(newLines, bulletLine)
		newLines = append(newLines, lines[i:]...)
		newBlock := strings.Join(newLines, "\n")
		return text[:start] + newBlock + body, true, nil
	}

	// No existing list-key — append a new section to end of block. Body
	// starts with `\n---` so we leave no trailing newline on newBlock to
	// avoid creating a blank line before the closing fence.
	trimmed := strings.TrimRight(block, "\n")
	newBlock := trimmed + "\n" + listKey + ":\n" + bulletLine
	return text[:start] + newBlock + body, true, nil
}

// removeBulletFromFrontMatter finds and removes a `  - "TARGET :: ..."` line
// in any of the three list-form fields. Returns (newText, removed).
func removeBulletFromFrontMatter(text, targetSlug string) (string, bool) {
	start, end := frontMatterBounds(text)
	if start < 0 {
		return text, false
	}
	block := text[start:end]
	body := text[end:]
	lines := strings.Split(block, "\n")

	for i, ln := range lines {
		s := strings.TrimRight(ln, " ")
		if !strings.HasPrefix(s, "  - ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(s, "  - "))
		payload = strings.TrimPrefix(strings.TrimSuffix(payload, `"`), `"`)
		if ref, ok := parseRelatedLine(payload); ok && ref.Slug == targetSlug {
			newLines := append([]string{}, lines[:i]...)
			newLines = append(newLines, lines[i+1:]...)
			newLines = dropEmptyListHeaders(newLines)
			newBlock := strings.Join(newLines, "\n")
			return text[:start] + newBlock + body, true
		}
	}
	return text, false
}

// dropEmptyListHeaders removes any `related_works:` / `related_theory:` /
// `pending_refs:` line whose immediately-following lines are NOT indented
// bullets — i.e., the list became empty after a removal. Keeps the front-
// matter tidy after related-rm calls.
func dropEmptyListHeaders(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if mm := scalarKey.FindStringSubmatch(ln); mm != nil && strings.TrimSpace(mm[2]) == "" {
			switch mm[1] {
			case "related_works", "related_theory", "pending_refs":
				// Peek ahead: empty list iff the next non-empty line isn't a bullet.
				hasBullet := false
				for j := i + 1; j < len(lines); j++ {
					t := strings.TrimRight(lines[j], " ")
					if t == "" {
						continue
					}
					hasBullet = strings.HasPrefix(t, "  - ")
					break
				}
				if !hasBullet {
					continue
				}
			}
		}
		out = append(out, ln)
	}
	return out
}

// setScalarInFrontMatter updates (or inserts) `field: value` in the front-
// matter block. Wraps value in double quotes when it contains `: ` (mapping-
// operator escape). Returns (newText, updated, err); updated is false if the
// existing value already matches.
func setScalarInFrontMatter(text, field, value string) (string, bool, error) {
	start, end := frontMatterBounds(text)
	if start < 0 {
		return text, false, fmt.Errorf("no front-matter in input")
	}
	block := text[start:end]
	body := text[end:]
	lines := strings.Split(block, "\n")

	encoded := value
	if field == "verified" {
		switch value {
		case "true", "false":
			// raw boolean
		default:
			return text, false, fmt.Errorf("verified must be 'true' or 'false' (got %q)", value)
		}
	} else {
		encoded = quoteValueIfNeeded(value)
	}
	newLine := field + ": " + encoded

	for i, ln := range lines {
		mm := scalarKey.FindStringSubmatch(ln)
		if mm == nil || mm[1] != field {
			continue
		}
		if ln == newLine {
			return text, false, nil
		}
		lines[i] = newLine
		return text[:start] + strings.Join(lines, "\n") + body, true, nil
	}
	// Field absent — insert before the first list-key (related_works /
	// related_theory / pending_refs), since list fields are conventionally
	// at the bottom of the front-matter and scalars cluster above. If no
	// list-key is present, append at the end of the block.
	insertAt := len(lines)
	for i, ln := range lines {
		if mm := scalarKey.FindStringSubmatch(ln); mm != nil {
			switch mm[1] {
			case "related_works", "related_theory", "pending_refs":
				insertAt = i
			}
			if insertAt != len(lines) {
				break
			}
		}
	}
	newLines := append([]string{}, lines[:insertAt]...)
	newLines = append(newLines, newLine)
	newLines = append(newLines, lines[insertAt:]...)
	return text[:start] + strings.Join(newLines, "\n") + body, true, nil
}

// keepImportSorted ensures the file imports stay in canonical order (used by
// gofmt downstream). It's a no-op placeholder so this file's imports list
// doesn't drift when adding helpers.
var _ = sort.Strings
