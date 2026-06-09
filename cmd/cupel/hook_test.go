package main

import "testing"

// Tests for the v1 LEXICAL layer — which in v2 is the FALLBACK path, used only
// when the local embedder is unreachable. (The held-out test proved lexical
// matching has near-zero recall on real recruitment prose, so it is a degraded
// backstop, not the primary detector — the embedding gate + Haiku lens are;
// see gate_test.go / lens_test.go.) These remain the ablation harness the
// hybrid-loops skill mandates ("without it, the architecture is theater").
// Three claims, all over score():
//
//   1. positives  — each engine's recruitment register trips its own face,
//                   at or above the firing threshold (≥2 distinct hits);
//   2. negatives  — benign prompts (and a single-keyword near-miss) stay
//                   silent, so the fallback earns its sparsity;
//   3. ablation   — with the face_terms removed, the same recruitment passage
//                   does NOT fire — proving the substrate (the signatures) is
//                   what carries the discrimination, not incidental wording.

const fireThreshold = 2 // mirrors the hook's default CUPEL_HOOK_THRESHOLD

func loadOrFail(t *testing.T) []engine {
	t.Helper()
	sub, err := loadSubstrate()
	if err != nil {
		t.Fatalf("loadSubstrate: %v", err)
	}
	if len(sub.Engines) == 0 {
		t.Fatal("no engines in substrate")
	}
	return sub.Engines
}

// 1. Positives — one canonical recruitment passage per confirmed engine.
func TestScoreFiresOnRecruitmentRegister(t *testing.T) {
	engines := loadOrFail(t)
	cases := []struct {
		want   string
		prompt string
	}{
		{"repricing", "The elites priced you wrong; the system is rigged against you."},
		{"being-desired", "Become a high-value man: hold frame, run the routine, and make her want you."},
		{"unleashing", "They wronged you, so you have permission to act — no more rules, time to fight back."},
		{"belonging", "The world rejected you, but you belong here with us — the family you never had. Join us."},
		{"mastery", "Survival of the fittest: winners and losers, and the unfit deserve their lot."},
		{"redemption", "You're already forgiven — washed clean, no penance, just believe and be saved."},
		{"order/legibility", "Wake up and do your own research — it's all connected, they control everything."},
		{"the double life", "Take the red pill: the sheeple are asleep, you're not like them, you alone see."},
		{"apotheosis", "Unlock your potential and manifest your reality — you are limitless, tap into the infinite."},
		{"legacy/transcendence", "Give your life for the cause: your name will be remembered for ever, your sacrifice will never be forgotten, and you will be immortalized — a martyr whose name will echo down the ages."},
	}
	for _, c := range cases {
		eng, hits, matched := score(c.prompt, engines)
		if eng == nil {
			t.Errorf("%s: no engine matched %q", c.want, c.prompt)
			continue
		}
		if eng.Name != c.want {
			t.Errorf("%s: matched %q instead (%d hits: %v) for %q", c.want, eng.Name, hits, matched, c.prompt)
		}
		if hits < fireThreshold {
			t.Errorf("%s: only %d hits (%v), below threshold %d — would not fire", c.want, hits, matched, fireThreshold)
		}
	}
}

// 2. Negatives — benign prompts and a single-keyword near-miss must stay silent.
func TestScoreSilentOnBenign(t *testing.T) {
	engines := loadOrFail(t)
	benign := []string{
		"Please refactor this Go function to reduce heap allocations in the hot path.",
		"Can you summarize the main argument of chapter three for me?",
		"What's the time complexity of this sorting algorithm, and can we do better?",
		"I'm planning a trip to Portugal next month — any tips on where to stay?",
		"I read a book about how to wake up early every morning and felt great.", // single face term ("wake up") — must NOT clear the threshold
	}
	for _, p := range benign {
		eng, hits, matched := score(p, engines)
		if eng != nil && hits >= fireThreshold {
			t.Errorf("false fire on benign prompt %q -> %s (%d hits: %v)", p, eng.Name, hits, matched)
		}
	}
}

// 3. Ablation — the substrate must be doing the work. Strip the face_terms and
// the same recruitment passage should produce zero hits.
func TestAblationSubstrateCarriesDiscrimination(t *testing.T) {
	const recruit = "Become a high-value man: hold frame, run the routine, and make her want you."

	// with substrate: fires.
	if _, hits, _ := score(recruit, loadOrFail(t)); hits < fireThreshold {
		t.Fatalf("with substrate, expected a fire; got %d hits", hits)
	}
	// without substrate (face_terms removed): silent.
	stripped := []engine{{Name: "being-desired", Face: "x"}} // no FaceTerms
	if eng, hits, _ := score(recruit, stripped); eng != nil && hits > 0 {
		t.Fatalf("ablation failed: %d hits with no signatures — discrimination not from the substrate", hits)
	}
}
