package main

// corpus_recall_test.go — THE FALSIFICATION TEST the v2 rewrite was built to
// pass. v1's lexical matcher fired on 0 of ~2,656 paragraphs of real
// recruitment-ancestor prose (Robison/Kybalion/Trine/Ovid/Sumner/Pericles) —
// that 0/2,656 is what killed v1. v2's whole premise is that recruitment
// register is *semantic*, so the embedding gate should catch what the lexical
// matcher missed. This test runs the EXACT live gate logic (loadOrBuildPrototypes
// + cosine + CUPEL_GATE_THRESHOLD) over those same texts and reports the recall.
//
// It is a MEASUREMENT, not a pass/fail unit test: it logs recall, the sim
// distribution, and the per-source own-engine hit rate. The one hard assertion
// is the minimal v2 claim — it must beat v1's literal zero. Run it with the real
// texts and a long timeout:
//
//   CUPEL_ANCESTOR_DIR=/tmp go test ./cmd/cupel -run TestRealCorpusGateRecall -v -timeout 600s
//
// Skips when the texts or the embedder are absent (so the suite stays green).

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// ancestorSources, gutenbergParagraphs, and strideSample moved to ancestors.go
// (non-test) so `cupel build-corpus` and this measurement share one extraction.

func embedInChunks(t *testing.T, texts []string, chunk int) [][]float64 {
	t.Helper()
	vs, err := embedTextsBatched(texts, chunk) // shared batcher (client.go)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return vs
}

func TestRealCorpusGateRecall(t *testing.T) {
	dir := os.Getenv("CUPEL_ANCESTOR_DIR")
	if dir == "" {
		t.Skip("set CUPEL_ANCESTOR_DIR to the dir holding the recruitment-ancestor pg*.txt to run the v2-recall measurement")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if _, err := embedTexts(ctx, []string{"probe"}); err != nil {
		cancel()
		t.Skipf("local embedder unreachable (%s @ %s): %v", embedModel(), ollamaURL(), err)
	}
	cancel()
	engines := loadOrFail(t)

	pctx, pcancel := context.WithTimeout(context.Background(), 60*time.Second)
	protos, err := loadOrBuildPrototypes(pctx, engines)
	pcancel()
	if err != nil {
		t.Fatalf("loadOrBuildPrototypes: %v", err)
	}
	thresh := gateThreshold()

	type srcStat struct {
		short    string
		engine   string
		n        int
		tripped  int // sim >= threshold to ANY engine
		ownClose int // best-match engine == its own engine
		ownTrip  int // tripped AND best-match is its own engine
		sims     []float64
	}
	var stats []srcStat
	var allSims []float64
	grandN, grandTrip := 0, 0
	type topHit struct {
		sim    float64
		engine string
		src    string
		text   string
	}
	var tops []topHit

	for _, src := range ancestorSources {
		raw, err := os.ReadFile(filepath.Join(dir, src.file))
		if err != nil {
			t.Logf("skip %s (%s): %v", src.short, src.file, err)
			continue
		}
		paras := gutenbergParagraphs(string(raw), 200)
		paras = strideSample(paras, envInt("CUPEL_ANCESTOR_MAX", 350))
		if len(paras) == 0 {
			continue
		}
		vecs := embedInChunks(t, paras, 64)
		st := srcStat{short: src.short, engine: src.engine, n: len(paras)}
		for i, v := range vecs {
			best, bestSim := "", -2.0
			for name, pv := range protos {
				if s := cosine(v, pv); s > bestSim {
					best, bestSim = name, s
				}
			}
			st.sims = append(st.sims, bestSim)
			allSims = append(allSims, bestSim)
			if best == src.engine {
				st.ownClose++
			}
			if bestSim >= thresh {
				st.tripped++
				if best == src.engine {
					st.ownTrip++
				}
				if len(tops) < 2000 {
					tops = append(tops, topHit{bestSim, best, src.short, paras[i]})
				}
			}
		}
		grandN += st.n
		grandTrip += st.tripped
		stats = append(stats, st)
	}

	if grandN == 0 {
		t.Skip("no ancestor paragraphs found in CUPEL_ANCESTOR_DIR")
	}

	pct := func(num, den int) float64 {
		if den == 0 {
			return 0
		}
		return 100 * float64(num) / float64(den)
	}
	sort.Float64s(allSims)
	q := func(p float64) float64 {
		if len(allSims) == 0 {
			return 0
		}
		idx := int(p * float64(len(allSims)-1))
		return allSims[idx]
	}

	t.Logf("=== v2 embedding-gate recall on real recruitment-ancestor prose (%s, thresh %.2f) ===", embedModel(), thresh)
	t.Logf("OVERALL: %d paragraphs, %d tripped the gate = %.1f%%  (v1 lexical was 0.0%%)", grandN, grandTrip, pct(grandTrip, grandN))
	t.Logf("sim distribution: p50=%.3f p75=%.3f p90=%.3f p99=%.3f max=%.3f", q(0.50), q(0.75), q(0.90), q(0.99), q(1.0))
	t.Logf("%-34s %-22s %6s %8s %9s %9s", "source", "→ engine", "paras", "tripped", "own-close", "own-trip")
	for _, s := range stats {
		t.Logf("%-34s %-22s %6d %7.1f%% %8.1f%% %8.1f%%",
			s.short, s.engine, s.n, pct(s.tripped, s.n), pct(s.ownClose, s.n), pct(s.ownTrip, s.n))
	}

	// sample the strongest hits to eyeball whether they are genuinely recruitment-y
	sort.Slice(tops, func(a, b int) bool { return tops[a].sim > tops[b].sim })
	t.Log("--- top firing paragraphs (sample) ---")
	for i := 0; i < len(tops) && i < 6; i++ {
		t.Logf("  %.3f [%s ← %s] %s", tops[i].sim, tops[i].engine, tops[i].src, snippet(tops[i].text, 140))
	}

	// the minimal v2 claim: beat v1's literal zero.
	if grandTrip == 0 {
		t.Errorf("v2 embedding gate ALSO fired 0/%d on real recruitment-ancestor prose — same recall hole as v1", grandN)
	}
}
