package main

// migrate_fm_quotes.go — `cupel migrate-fm-quotes`: one-shot migration that
// wraps scalar values and bullet items in works/*.md YAML front-matter so
// real yaml.v3 (KnownFields-strict) can parse them. The pre-migration
// convention used pseudo-YAML the hand-rolled splitFrontMatter tolerated:
//   - `source: Title: Subtitle` — unquoted `: ` is a YAML mapping operator
//   - `- slug :: gloss` bullets — yaml parses this as `{slug: ": gloss"}` not a string
//   - `work: Wolf Hall (Cromwell trilogy: Wolf Hall, ...)` — same `: ` problem
// After this migration, every problematic value is double-quoted, so
// `cupel db-sync` can use yaml.v3 strict decode and surface typos at commit
// time. Idempotent — runs cleanly against already-migrated files.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	scalarLine = regexp.MustCompile(`^([a-zA-Z_]+):\s*(.*)$`)
	bulletLine = regexp.MustCompile(`^(\s+- )(.+)$`)
)

func runMigrateFMQuotes(args []string) {
	fs := flag.NewFlagSet("migrate-fm-quotes", flag.ExitOnError)
	dir := fs.String("dir", "works", "directory of works/*.md to migrate")
	apply := fs.Bool("apply", false, "write changes (default: dry-run; report only)")
	_ = fs.Parse(args)

	matches, _ := filepath.Glob(filepath.Join(*dir, "*.md"))
	changed := 0
	for _, path := range matches {
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate-fm-quotes: read %s: %v\n", path, err)
			continue
		}
		text := strings.ReplaceAll(string(data), "\r", "")
		newText, n := requoteFrontMatter(text)
		if n == 0 {
			continue
		}
		changed++
		fmt.Fprintf(os.Stderr, "%s — %d line(s) requoted\n", path, n)
		if *apply {
			if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "migrate-fm-quotes: write %s: %v\n", path, err)
			}
		}
	}
	mode := "would change"
	if *apply {
		mode = "changed"
	}
	fmt.Fprintf(os.Stderr, "migrate-fm-quotes: %s %d file(s)\n", mode, changed)
}

// requoteFrontMatter walks the YAML front-matter block and wraps in double
// quotes every (a) top-level scalar value containing `: ` (the YAML mapping
// operator) outside flow-style brackets, and (b) every bullet item under a
// list-opener (the `- slug :: gloss` form, all of which need quoting because
// `::` parses as `:` + `:` in real YAML). Returns the new text and the count
// of lines requoted. Idempotent on already-quoted values.
func requoteFrontMatter(text string) (string, int) {
	if !strings.HasPrefix(text, "---") {
		return text, 0
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return text, 0
	}
	block := text[3 : 3+end]
	body := text[3+end:]
	lines := strings.Split(block, "\n")
	out := make([]string, len(lines))
	n := 0
	for i, line := range lines {
		switch {
		case scalarLine.MatchString(line):
			mm := scalarLine.FindStringSubmatch(line)
			key, val := mm[1], strings.TrimRight(mm[2], " ")
			if needsScalarQuoting(val) {
				out[i] = key + ": " + yamlQuote(val)
				n++
			} else {
				out[i] = line
			}
		case bulletLine.MatchString(line):
			mm := bulletLine.FindStringSubmatch(line)
			prefix, val := mm[1], strings.TrimRight(mm[2], " ")
			if needsBulletQuoting(val) {
				out[i] = prefix + yamlQuote(val)
				n++
			} else {
				out[i] = line
			}
		default:
			out[i] = line
		}
	}
	return text[:3] + strings.Join(out, "\n") + body, n
}

// needsScalarQuoting: true iff value contains `: ` (the mapping operator)
// outside of flow-style brackets and isn't already a quoted/flow/empty value.
func needsScalarQuoting(val string) bool {
	if val == "" {
		return false // list-opener
	}
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return false // already double-quoted
	}
	if strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) {
		return false // single-quoted
	}
	if strings.HasPrefix(val, "[") || strings.HasPrefix(val, "{") {
		return false // flow-style list or map — leave to yaml.v3
	}
	return strings.Contains(val, ": ")
}

// needsBulletQuoting: bullets under list-openers are all `- slug :: gloss`
// shape today, which is invalid YAML (parses as a map). Quote any bullet
// whose content isn't already quoted.
func needsBulletQuoting(val string) bool {
	if val == "" {
		return false
	}
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return false
	}
	if strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) {
		return false
	}
	// Bullets that don't carry the `slug :: gloss` form (e.g. a queued_engines
	// scalar `- belonging`) only need quoting if they contain `: `.
	if !strings.Contains(val, " :: ") {
		return strings.Contains(val, ": ")
	}
	return true
}

// yamlQuote wraps a value in double quotes, escaping embedded `"` and `\`.
func yamlQuote(val string) string {
	esc := strings.ReplaceAll(val, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `"` + esc + `"`
}
