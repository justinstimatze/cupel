package main

// ancestors.go — the recruitment-ancestor corpus: the real prose the v1-recall
// failure was measured on, turned into a labeled held-out corpus for the v2.1
// critic (the v2.2 "honest grounding" path). The extraction (gutenbergParagraphs,
// strideSample) and the source→engine mapping (ancestorSources) live here, shared
// by `cupel build-corpus` and corpus_recall_test.go's TestRealCorpusGateRecall.
//
// `cupel build-corpus` emits a JSON corpus that CUPEL_CRITIC_CORPUS can point at,
// so `cupel critic` calibrates on real recruitment prose instead of the synthetic
// builtinProbeCorpus seed (probe.go), which it can otherwise overfit to. The texts
// are public-domain Gutenberg, ephemeral in /tmp (re-downloaded each session), so
// the *builder* — not the generated JSON — is the committed reproducibility artifact.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ancestorSources maps each recruitment-ancestor text (Gutenberg id) to the
// engine whose dual-use FACE it is the historical ancestor of. This is the
// documented dual-use mapping (see entries/_dual-use.md / HANDOFF).
var ancestorSources = []struct {
	file   string
	engine string
	short  string
}{
	{"pg47605.txt", "order/legibility", "Robison: Proofs of a Conspiracy"},
	{"pg14209.txt", "the double life", "The Kybalion"},
	{"pg23559.txt", "apotheosis", "Trine: In Tune with the Infinite"},
	{"pg47677.txt", "being-desired", "Ovid: Ars Amatoria"},
	{"pg7142.txt", "legacy/transcendence", "Thucydides (Pericles)"},
	{"pg18603.txt", "mastery", "Sumner: What Social Classes Owe"},
	{"pg68185.txt", "purity/contamination", "Grant: The Passing of the Great Race"},
	{"pg3207.txt", "security/safety", "Hobbes: Leviathan"},
}

// strideSample evenly subsamples to at most max items (even coverage across the
// whole text, not just the front), so the measurement stays feasible: ollama
// embeds serially at ~60ms/paragraph, and the full texts run to >10k paragraphs.
func strideSample(xs []string, max int) []string {
	if max <= 0 || len(xs) <= max {
		return xs
	}
	out := make([]string, 0, max)
	step := float64(len(xs)) / float64(max)
	for i := 0; i < max; i++ {
		out = append(out, xs[int(float64(i)*step)])
	}
	return out
}

// gutenbergParagraphs strips the PG boilerplate and splits the body into
// paragraphs (blank-line separated, wrapped lines rejoined), keeping only
// substantial ones (≥ minLen chars) — the dry single-line headers and the
// front/back matter are not what we are testing.
func gutenbergParagraphs(raw string, minLen int) []string {
	raw = strings.ReplaceAll(raw, "\r", "")
	if i := strings.Index(raw, "*** START OF"); i >= 0 {
		if nl := strings.IndexByte(raw[i:], '\n'); nl >= 0 {
			raw = raw[i+nl+1:]
		}
	}
	if j := strings.Index(raw, "*** END OF"); j >= 0 {
		raw = raw[:j]
	}
	var out []string
	for _, block := range strings.Split(raw, "\n\n") {
		p := strings.TrimSpace(strings.Join(strings.Fields(block), " "))
		if len(p) >= minLen {
			out = append(out, p)
		}
	}
	return out
}

// buildAncestorCorpus turns the ancestor texts in dir into a labeled critic
// corpus: real recruitment-register POS per engine (stride-sampled to maxPerEngine)
// plus the realistic modern-benign NEG from builtinProbeCorpus — the register the
// live gate must NOT fire on. A missing ancestor text is fine; that engine is just
// left ungrounded (the critic leaves it at the OwnFloor=2 sentinel, never flagged).
// With fillSynthetic, engines with no real ancestor text fall back to their builtin
// synthetic POS so every engine is covered. Returns the corpus and the set of
// engines that got REAL (not synthetic) grounding, for an honest summary.
func buildAncestorCorpus(dir string, maxPerEngine int, fillSynthetic bool) ([]probeExample, map[string]bool, error) {
	var corpus []probeExample
	realCovered := map[string]bool{}
	for _, src := range ancestorSources {
		raw, err := os.ReadFile(filepath.Join(dir, src.file))
		if err != nil {
			continue // ungrounded engine — see doc above
		}
		paras := strideSample(gutenbergParagraphs(string(raw), 200), maxPerEngine)
		for _, p := range paras {
			corpus = append(corpus, probeExample{Engine: src.engine, Text: p})
		}
		if len(paras) > 0 {
			realCovered[src.engine] = true
		}
	}

	// benign negatives — always included (the live gate's real false-positive bed)
	for _, ex := range builtinProbeCorpus {
		if ex.Engine == "" {
			corpus = append(corpus, ex)
		}
	}

	if fillSynthetic {
		for _, ex := range builtinProbeCorpus {
			if ex.Engine != "" && !realCovered[ex.Engine] {
				corpus = append(corpus, ex)
			}
		}
	}

	if len(realCovered) == 0 && !fillSynthetic {
		return nil, nil, fmt.Errorf("no ancestor texts found in %s — check --dir (expected pg*.txt like %s)", dir, ancestorSources[0].file)
	}
	return corpus, realCovered, nil
}

// runBuildCorpus is `cupel build-corpus`: regenerate the held-out critic corpus
// from the local recruitment-ancestor texts. Pure text — no ollama, no API key.
func runBuildCorpus(args []string) {
	defaultDir := os.Getenv("CUPEL_ANCESTOR_DIR")
	if defaultDir == "" {
		defaultDir = "/tmp"
	}
	fs := flag.NewFlagSet("build-corpus", flag.ContinueOnError)
	dirF := fs.String("dir", defaultDir, "dir holding the ancestor pg*.txt")
	outF := fs.String("out", "", "output path for the JSON corpus (default: stdout)")
	maxF := fs.Int("max", 50, "per-engine cap on sampled paragraphs")
	fillF := fs.Bool("fill-synthetic", false, "cover engines with no ancestor text from the synthetic seed")
	if err := fs.Parse(args); err != nil {
		return
	}
	dir, out, maxPer, fill := *dirF, *outF, *maxF, *fillF

	corpus, real, err := buildAncestorCorpus(dir, maxPer, fill)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cupel build-corpus:", err)
		os.Exit(1)
	}

	b, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cupel build-corpus: marshal:", err)
		os.Exit(1)
	}

	// Pure JSON to stdout when streaming (so it pipes); the summary then goes to
	// stderr. With --out, the file gets the JSON and the summary goes to stdout.
	summaryW := os.Stdout
	if out == "" {
		if _, err := os.Stdout.Write(b); err != nil {
			fmt.Fprintln(os.Stderr, "cupel build-corpus: write:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout)
		summaryW = os.Stderr
	} else {
		if err := os.WriteFile(out, b, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "cupel build-corpus: write", out+":", err)
			os.Exit(1)
		}
	}

	// per-engine summary (POS by engine, marked real vs synthetic; then negatives)
	counts := map[string]int{}
	for _, ex := range corpus {
		counts[ex.Engine]++
	}
	var engines []string
	for name := range counts {
		if name != "" {
			engines = append(engines, name)
		}
	}
	sort.Strings(engines)

	fmt.Fprintln(summaryW, "cupel build-corpus — held-out critic corpus from real recruitment-ancestor prose")
	fmt.Fprintf(summaryW, "  dir: %s   per-engine cap: %d   fill-synthetic: %v\n", dir, maxPer, fill)
	for _, name := range engines {
		tag := "synthetic"
		if real[name] {
			tag = "real"
		}
		fmt.Fprintf(summaryW, "  %-24s %4d POS  (%s)\n", name, counts[name], tag)
	}
	fmt.Fprintf(summaryW, "  %-24s %4d NEG  (benign)\n", "(negatives)", counts[""])
	if len(real) == 0 {
		fmt.Fprintln(summaryW, "  WARNING: no engine got real ancestor grounding — only synthetic/negatives present.")
	}
	dest := out
	if dest == "" {
		dest = "stdout"
	}
	fmt.Fprintf(summaryW, "  total: %d examples → %s\n", len(corpus), dest)
	fmt.Fprintln(summaryW, "  point the critic at it:  CUPEL_CRITIC_CORPUS="+dest+" cupel critic")
}
