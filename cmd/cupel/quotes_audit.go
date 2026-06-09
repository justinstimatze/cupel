package main

// quotes_audit.go — deterministic regex-based detector for the cupel
// quotes-only rule: every cite anchor must be a verbatim quote from source —
// no paraphrase in citation position. Pure text: no API.
//
// What it flags: a parenthetical containing `works/<slug>.md` whose
// non-quoted, non-structural prose exceeds the threshold (default 12 chars).
// Allowed inside the parens: quoted spans ("...", curly quotes), speaker
// attributions (Word:), location markers (Ch. N, Stave V, Act II, S2E3,
// p. 42, l. 1080), the cross-ref itself, light markdown, punctuation.
// Anything else is treated as paraphrase risk.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	citeParenRe = regexp.MustCompile(`\(([^()]*\bworks/[a-z0-9-]+\.md\b[^()]*)\)`)
	quotedRe    = regexp.MustCompile("[\"“‘][^\"“”‘’]*[\"”’]")
	backtickRe  = regexp.MustCompile("`[^`]*`")
	mdEmphRe    = regexp.MustCompile(`[*_]+`)
	worksRe     = regexp.MustCompile(`\bworks/[a-z0-9-]+\.md\b`)
	theoryRe    = regexp.MustCompile(`\btheory/[a-z0-9-]+\.md\b`)

	// Structural tokens that are allowed in a cite parenthetical without
	// counting as paraphrase. Order matters — longer / more specific first.
	structuralRes = []*regexp.Regexp{
		regexp.MustCompile(`\bS\d+E\d+\b`),
		regexp.MustCompile(`\b(?:Chapter|Stave|Act|Scene|Book|Volume|Vol|Section|sect|Canto|Part|Episode|Ep)\.?\s*[IVXLCDMivxlcdm0-9]+(?:[-–][IVXLCDMivxlcdm0-9]+)?`),
		regexp.MustCompile(`\b(?:Ch|ch|pp|p|ll|l|¶|n)\.\s*\d+(?:[-–]\d+)?`),
		regexp.MustCompile(`\b[A-Z][a-zA-Z'’-]{1,25}(?:\s+(?:of|the|to|and|de|von|van)\s+[A-Z][a-zA-Z'’-]+){0,2}:`), // Speaker:
		regexp.MustCompile(`\b(?:foreword|preface|epilogue|prologue|introduction|afterword)\b`),
		// Cupel project vocabulary — cross-ref introducers and structural tags allowed in cite parens.
		regexp.MustCompile(`\b(?:see|per|via|after|cf|compare|paired\s+with|sibling\s+to|alongside|alongside\s+with|cross-ref)\b\.?`),
		regexp.MustCompile(`\b(?:slot-proven|slot-tested|slot-test|wikipedia-grounded|outside-critique|partial-refusal|pure-counterfeit|counterfeit-pole|enabling-pole|refusal-pole|canonical-text|canonical-text-graduated|trainable-craft|prophet-gift|bistable|bistable-braid)\b`),
		regexp.MustCompile(`\b[a-z][a-z-]+-(?:pole|mode|register|cluster|sub-mode|sub-register)\b`),
		regexp.MustCompile(`\b(?:Gutenberg|PG)\s*#?\d+\b`),
		regexp.MustCompile(`\bnow\s+slot-proven\b`),
		regexp.MustCompile(`\bacross\s+\w+\s+specimens?\b`),
	}

	punctRe = regexp.MustCompile(`[\s,;:.\-—–()*_'"` + "`" + `“”‘’]+`)
)

type quotesViolation struct {
	Path    string
	Line    int
	Cite    string // the full parenthetical, including ( )
	Residue string // what's left after stripping quotes, structural tokens
}

func runQuotesAudit(args []string) {
	fs := flag.NewFlagSet("quotes-audit", flag.ContinueOnError)
	root := fs.String("dir", ".", "directory to scan recursively for .md files")
	target := fs.String("target", "", "single file to scan (overrides --dir); default scans README.md, works/, theory/")
	threshold := fs.Int("threshold", 12, "max chars of non-quoted, non-structural prose per cite parenthetical")
	quiet := fs.Bool("quiet", false, "print only violations (no summary line per file)")
	if err := fs.Parse(args); err != nil {
		return
	}

	var files []string
	if *target != "" {
		files = []string{*target}
	} else {
		files = collectAuditTargets(*root)
	}

	violations := 0
	for _, path := range files {
		v := auditFile(path, *threshold)
		if !*quiet && len(v) > 0 {
			fmt.Printf("%s — %d violation(s)\n", path, len(v))
		}
		for _, vio := range v {
			fmt.Printf("  %s:%d  residue=%q\n", vio.Path, vio.Line, vio.Residue)
			fmt.Printf("    cite: %s\n", trimForDisplay(vio.Cite, 160))
		}
		violations += len(v)
	}
	if violations > 0 {
		fmt.Fprintf(os.Stderr, "\nquotes-audit: %d violation(s) across %d file(s)\n", violations, len(files))
		os.Exit(1)
	}
	if !*quiet {
		fmt.Printf("quotes-audit: clean across %d file(s)\n", len(files))
	}
}

func collectAuditTargets(root string) []string {
	var out []string
	roots := []string{filepath.Join(root, "README.md"), filepath.Join(root, "works"), filepath.Join(root, "theory")}
	for _, r := range roots {
		info, err := os.Stat(r)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			out = append(out, r)
			continue
		}
		filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, ".md") {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func auditFile(path string, threshold int) []quotesViolation {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []quotesViolation
	for _, match := range citeParenRe.FindAllStringSubmatchIndex(string(raw), -1) {
		full := string(raw[match[0]:match[1]])
		inner := string(raw[match[2]:match[3]])
		residue := residueAfterStructural(inner)
		if len(residue) > threshold {
			out = append(out, quotesViolation{
				Path:    path,
				Line:    1 + strings.Count(string(raw[:match[0]]), "\n"),
				Cite:    full,
				Residue: residue,
			})
		}
	}
	return out
}

func residueAfterStructural(s string) string {
	s = quotedRe.ReplaceAllString(s, "")
	s = backtickRe.ReplaceAllString(s, "")
	s = mdEmphRe.ReplaceAllString(s, "")
	s = worksRe.ReplaceAllString(s, "")
	s = theoryRe.ReplaceAllString(s, "")
	for _, re := range structuralRes {
		s = re.ReplaceAllString(s, "")
	}
	s = punctRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func trimForDisplay(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
