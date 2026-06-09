package main

// Tests for the v2 GATE (the primary detector's cheap half). Skipped when the
// local embedder is unreachable, so the suite stays green offline. The core
// claim — the gate's reason for existing over v1 lexical — is SEPARATION:
// recruitment register sits closer to some engine prototype than benign prose
// does, by a clean margin. This is also the ablation (does the embedding
// substrate carry the discrimination?): on a held-out set the prototypes never
// appear in, POS_min must exceed NEG_max.
//
// SCOPE — what this proves and what it does NOT. This is a RECRUITMENT-vs-BENIGN
// recall claim: the gate's whole job is to TRIP on recruitment register and stay
// quiet on benign work; gateScore returns the best-matching engine's cosine, so
// POS_min > NEG_max means "recruitment outscores benign against SOME prototype."
// It is NOT a per-engine ATTRIBUTION claim — that the right engine wins — which
// is the LENS's job, not the gate's. The v2.1 critic's per-engine margin
// (ownFloor − confusionCeiling, where the ceiling includes OTHER engines' POS) is
// the attribution measure, and on real recruitment-ancestor prose it goes
// strongly negative (cmd/cupel/ancestors.go / HANDOFF): the gate over-fires the
// wrong engine, and the lens reassigns. Two different properties; do not read this
// test's pass as "the prototypes classify by engine." Also: POS here are crafted
// synthetic sentences, so POS_min > threshold asserts ~100% recall on THAT set;
// real-prose recall is ~84% (corpus_recall_test.go) — the synthetic guarantee is
// tighter than reality, by design (recall-first, the lens rejects false trips).

import (
	"context"
	"testing"
	"time"
)

func gateOrSkip(t *testing.T) []engine {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := embedTexts(ctx, []string{"probe"}); err != nil {
		t.Skipf("local embedder unreachable (%s @ %s): %v", embedModel(), ollamaURL(), err)
	}
	return loadOrFail(t)
}

func TestGateSeparatesRecruitmentFromBenign(t *testing.T) {
	engines := gateOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Held-out — the shared labeled corpus (probe.go), single source of truth
	// with the v2.1 critic's ablation. None of these phrasings appear in the
	// prototypes. POS are the recruitment examples; NEG the benign ones.
	var pos []string
	for _, ex := range probePositives() {
		pos = append(pos, ex.Text)
	}
	neg := probeNegatives()

	posMin, negMax := 2.0, -2.0
	for _, p := range pos {
		_, s, err := gateScore(ctx, p, engines)
		if err != nil {
			t.Fatalf("gateScore: %v", err)
		}
		if s < posMin {
			posMin = s
		}
	}
	for _, p := range neg {
		_, s, err := gateScore(ctx, p, engines)
		if err != nil {
			t.Fatalf("gateScore: %v", err)
		}
		if s > negMax {
			negMax = s
		}
	}
	t.Logf("gate separation (%s): POS_min=%.3f  NEG_max=%.3f  margin=%+.3f  threshold=%.3f",
		embedModel(), posMin, negMax, posMin-negMax, gateThreshold())

	// 1. Separation / ablation: recruitment is strictly closer to a prototype.
	if posMin <= negMax {
		t.Errorf("gate does not separate: POS_min=%.3f <= NEG_max=%.3f", posMin, negMax)
	}
	// 2. The shipped default threshold actually sits in the gap (recall-first:
	//    every POS clears it, every clear-benign falls below).
	if gateThreshold() > posMin {
		t.Errorf("threshold %.3f above POS_min %.3f — would miss recruitment (recall loss)", gateThreshold(), posMin)
	}
	if gateThreshold() <= negMax {
		t.Errorf("threshold %.3f at/below NEG_max %.3f — would fire on benign work", gateThreshold(), negMax)
	}
}
