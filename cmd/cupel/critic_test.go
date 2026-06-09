package main

// Tests for the v2.1 CRITIC. The deterministic half (diagnose + ablate) needs
// only the local embedder; the proposal half needs a key. Both skip when their
// dep is absent, so the suite stays green offline. The load-bearing claim is the
// ABLATION GATE: the critic must REFUSE an edit the held-out corpus doesn't
// endorse — that refusal is what separates this loop from theater.

import (
	"context"
	"math"
	"testing"
	"time"
)

func criticOrSkip(t *testing.T) ([]engine, map[string][]float64, []engineDiag) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := embedTexts(ctx, []string{"probe"}); err != nil {
		t.Skipf("local embedder unreachable (%s @ %s): %v", embedModel(), ollamaURL(), err)
	}
	engines := loadOrFail(t)
	diags, corpus, err := diagnoseEngines(ctx, engines)
	if err != nil {
		t.Fatalf("diagnoseEngines: %v", err)
	}
	return engines, corpus, diags
}

func TestDiagnoseComputesMargins(t *testing.T) {
	engines, corpus, diags := criticOrSkip(t)

	if len(diags) != len(engines) {
		t.Errorf("diagnosed %d engines, expected %d (every confirmed engine has a prototype)", len(diags), len(engines))
	}
	for _, d := range diags {
		if d.OwnFloor < -1.01 || d.OwnFloor > 1.01 {
			t.Errorf("%s: own_floor %.3f out of cosine range", d.Name, d.OwnFloor)
		}
		if d.ConfusionCeiling < -1.01 || d.ConfusionCeiling > 1.01 {
			t.Errorf("%s: confusion_ceiling %.3f out of cosine range", d.Name, d.ConfusionCeiling)
		}
		if math.Abs(d.Margin-(d.OwnFloor-d.ConfusionCeiling)) > 1e-9 {
			t.Errorf("%s: margin %.3f != own_floor-ceiling %.3f", d.Name, d.Margin, d.OwnFloor-d.ConfusionCeiling)
		}
		if len(d.LowExamples) == 0 || len(d.HighConfusers) == 0 {
			t.Errorf("%s: missing evidence (low=%d high=%d)", d.Name, len(d.LowExamples), len(d.HighConfusers))
		}
	}
	// sorted weakest-first (the flag order)
	for i := 1; i < len(diags); i++ {
		if diags[i].Margin < diags[i-1].Margin {
			t.Errorf("diags not sorted weakest-first at %d (%.3f < %.3f)", i, diags[i].Margin, diags[i-1].Margin)
		}
	}
	// the corpus vectors the ablation reuses are present
	if len(corpus) == 0 {
		t.Error("diagnose returned no corpus vectors")
	}
}

func TestAblationRejectsWorsePrototype(t *testing.T) {
	_, corpus, diags := criticOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d := diags[0] // any engine; the weakest

	// A deliberately off-register, benign "prototype": recruitment prose will sit
	// far from it (own recall collapses), so the ablation MUST reject it.
	benign := "A short technical note on configuring HTTP retry backoff and choosing B-tree versus LSM-tree indexes for a write-heavy web service."
	ab, err := ablateProposal(ctx, d.Name, benign, corpus, d)
	if err != nil {
		t.Fatalf("ablateProposal: %v", err)
	}
	if ab.Accepted {
		t.Errorf("ablation ACCEPTED a benign off-register prototype for %s (margin %.3f→%.3f) — the gate is not protecting separation",
			d.Name, d.Margin, ab.MarginAfter)
	}
	if ab.Reason == "" {
		t.Error("rejected ablation carries no reason")
	}
	t.Logf("benign prototype for %s correctly rejected: %s", d.Name, ab.Reason)
}

// TestProposeAndAblate exercises the LLM proposal → ablation path for one engine.
// Skips without a key; cached on disk, so it costs one API call ever.
func TestProposeAndAblate(t *testing.T) {
	key := resolveAPIKey()
	if key == "" {
		t.Skip("no ANTHROPIC_API_KEY reachable (env / CUPEL_ENV_FILE / ~/.claude/cupel/.env)")
	}
	engines, corpus, diags := criticOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d := diags[0]
	var e *engine
	for i := range engines {
		if engines[i].Name == d.Name {
			e = &engines[i]
		}
	}
	if e == nil {
		t.Fatalf("engine %q not found", d.Name)
	}
	prop, _, err := proposePrototype(ctx, key, e, d)
	if err != nil {
		t.Fatalf("proposePrototype: %v", err)
	}
	if prop.Prototype == "" {
		t.Fatal("proposal returned an empty prototype")
	}
	ab, err := ablateProposal(ctx, d.Name, prop.Prototype, corpus, d)
	if err != nil {
		t.Fatalf("ablateProposal: %v", err)
	}
	if ab.Reason == "" {
		t.Error("ablation carries no reason")
	}
	t.Logf("proposal for %s: accepted=%v  %s  (rationale: %s)", d.Name, ab.Accepted, ab.Reason, prop.Rationale)
}
