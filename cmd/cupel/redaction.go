package main

// redaction.go — generic content-redaction audit. Two surfaces:
//
//   cupel redaction-audit          scan tracked .md files against the
//                                  project-local redaction-pattern set;
//                                  exit 1 if any hits (pre-commit gate)
//   cupel redaction-hook           Claude Code PreToolUse:Write|Edit hook;
//                                  reads JSON from stdin; exit 2 (block)
//                                  if the proposed content for a scoped
//                                  tracked file matches any pattern;
//                                  exit 0 otherwise
//
// Patterns are loaded at runtime from a project-local config file at
//   $CUPEL_REDACTION_PATTERNS  (default: ./.cupel/redaction-patterns.txt)
// Each non-blank, non-comment line is `name|regex`. Patterns are applied
// case-insensitively. If the file is absent or unreadable, the audit and
// the hook no-op silently — a fresh clone gets neither protection nor
// noise until the project owner provides a pattern set.
//
// Scope (which files are protected from leaks):
//   README.md, theory/**/*.md, works/**/*.md
// Explicitly out of scope: gitignored docs and binary/data files.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type redactionRule struct {
	Name string
	Re   *regexp.Regexp
}

func loadRedactionRules() []redactionRule {
	path := os.Getenv("CUPEL_REDACTION_PATTERNS")
	if path == "" {
		path = filepath.Join(".cupel", "redaction-patterns.txt")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rules []redactionRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '|')
		if i <= 0 || i == len(line)-1 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		expr := strings.TrimSpace(line[i+1:])
		re, err := regexp.Compile(`(?i)` + expr)
		if err != nil {
			continue
		}
		rules = append(rules, redactionRule{Name: name, Re: re})
	}
	return rules
}

type redactionHit struct {
	Path    string
	Line    int
	Pattern string
	Excerpt string
}

func runRedactionAudit(args []string) {
	fs := flag.NewFlagSet("redaction-audit", flag.ContinueOnError)
	root := fs.String("dir", ".", "directory to scan recursively")
	target := fs.String("target", "", "single file to scan (overrides --dir)")
	quiet := fs.Bool("quiet", false, "print only hits (no per-file summary)")
	if err := fs.Parse(args); err != nil {
		return
	}

	rules := loadRedactionRules()
	if len(rules) == 0 {
		if !*quiet {
			fmt.Println("redaction-audit: no patterns configured (set CUPEL_REDACTION_PATTERNS or populate .cupel/redaction-patterns.txt)")
		}
		return
	}

	var files []string
	if *target != "" {
		files = []string{*target}
	} else {
		files = collectRedactionTargets(*root)
	}

	total := 0
	for _, path := range files {
		hits := scanRedactionFile(path, rules)
		if !*quiet && len(hits) > 0 {
			fmt.Printf("%s — %d hit(s)\n", path, len(hits))
		}
		for _, h := range hits {
			fmt.Printf("  %s:%d  pattern=%s\n", h.Path, h.Line, h.Pattern)
			fmt.Printf("    %s\n", trimForDisplay(h.Excerpt, 160))
		}
		total += len(hits)
	}
	if total > 0 {
		fmt.Fprintf(os.Stderr, "\nredaction-audit: %d hit(s) across %d file(s)\n", total, len(files))
		os.Exit(1)
	}
	if !*quiet {
		fmt.Printf("redaction-audit: clean across %d file(s)\n", len(files))
	}
}

func collectRedactionTargets(root string) []string {
	excludedBasenames := map[string]bool{
		"HANDOFF.md":            true,
		"wanted-materials.md":   true,
		"acquired-materials.md": true,
	}
	var out []string
	roots := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "theory"),
		filepath.Join(root, "entries"),
		filepath.Join(root, "reviews"),
	}
	for _, r := range roots {
		info, err := os.Stat(r)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if !excludedBasenames[filepath.Base(r)] {
				out = append(out, r)
			}
			continue
		}
		filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".md") {
				return nil
			}
			base := filepath.Base(p)
			if excludedBasenames[base] {
				return nil
			}
			if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") {
				return nil
			}
			out = append(out, p)
			return nil
		})
	}
	return out
}

func scanRedactionFile(path string, rules []redactionRule) []redactionHit {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return scanRedactionContent(path, string(raw), rules)
}

func scanRedactionContent(path, content string, rules []redactionRule) []redactionHit {
	var out []redactionHit
	for _, r := range rules {
		for _, idx := range r.Re.FindAllStringIndex(content, -1) {
			line := 1 + strings.Count(content[:idx[0]], "\n")
			start := idx[0] - 30
			if start < 0 {
				start = 0
			}
			end := idx[1] + 30
			if end > len(content) {
				end = len(content)
			}
			out = append(out, redactionHit{
				Path:    path,
				Line:    line,
				Pattern: r.Name,
				Excerpt: content[start:end],
			})
		}
	}
	return out
}

// ---- PreToolUse hook ----

type redactionHookInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type redactionWriteInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type redactionEditInput struct {
	FilePath  string `json:"file_path"`
	NewString string `json:"new_string"`
}

type redactionMultiEditInput struct {
	FilePath string `json:"file_path"`
	Edits    []struct {
		NewString string `json:"new_string"`
	} `json:"edits"`
}

func runRedactionHook() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(0)
	}
	var in redactionHookInput
	if json.Unmarshal(raw, &in) != nil {
		os.Exit(0)
	}

	var filePath, newContent string
	switch in.ToolName {
	case "Write":
		var w redactionWriteInput
		if json.Unmarshal(in.ToolInput, &w) != nil {
			os.Exit(0)
		}
		filePath = w.FilePath
		newContent = w.Content
	case "Edit":
		var e redactionEditInput
		if json.Unmarshal(in.ToolInput, &e) != nil {
			os.Exit(0)
		}
		filePath = e.FilePath
		newContent = e.NewString
	case "MultiEdit":
		var m redactionMultiEditInput
		if json.Unmarshal(in.ToolInput, &m) != nil {
			os.Exit(0)
		}
		filePath = m.FilePath
		var b strings.Builder
		for _, ed := range m.Edits {
			b.WriteString(ed.NewString)
			b.WriteString("\n")
		}
		newContent = b.String()
	default:
		os.Exit(0)
	}

	if !redactionInScope(filePath) {
		os.Exit(0)
	}

	rules := loadRedactionRules()
	if len(rules) == 0 {
		os.Exit(0)
	}

	hits := scanRedactionContent(filePath, newContent, rules)
	if len(hits) == 0 {
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "cupel redaction-hook: blocked %s — proposed content matches configured redaction pattern(s):\n", filePath)
	for _, h := range hits {
		fmt.Fprintf(os.Stderr, "  pattern=%s\n", h.Pattern)
	}
	fmt.Fprintln(os.Stderr, "  (rewrite to remove matched content, then retry)")
	os.Exit(2)
}

func redactionInScope(file string) bool {
	if file == "" {
		return false
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	base := filepath.Base(abs)
	if base == "HANDOFF.md" || base == "wanted-materials.md" || base == "acquired-materials.md" {
		return false
	}
	if strings.HasPrefix(base, "session-handoff") || strings.HasPrefix(base, "loop-session-handoff") {
		return false
	}
	if !strings.HasSuffix(abs, ".md") {
		return false
	}
	if base == "README.md" {
		return true
	}
	for _, seg := range []string{
		string(filepath.Separator) + "theory" + string(filepath.Separator),
		string(filepath.Separator) + "entries" + string(filepath.Separator),
		string(filepath.Separator) + "reviews" + string(filepath.Separator),
	} {
		if strings.Contains(abs, seg) {
			return true
		}
	}
	return false
}
