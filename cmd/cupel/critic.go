package main

// critic.go — the v2.1 STACKED LOOP: a development-time hybrid loop that wraps
// the runtime. It reads the runtime's evidence (the held-out separation corpus
// now; fires.jsonl + verdicts as they accumulate) and proposes hardening edits
// to each engine's `prototype` — but ONLY when a deterministic ablation proves
// the edit widens separation. It never mutates the curated engines.json; it
// writes proposals for human review (`--apply` emits a candidate substrate to a
// sibling path to diff).
//
//   LENS/REASONER  Haiku proposes a tighter prototype for a flagged engine.
//   GATE (ablation) re-embed it; accept iff margin improves, no benign example
//                   crosses the gate, and own-recall does not regress.
//   ACTION          write the proposal (accepted or rejected) + a margin delta.
//   CALIBRATION     the proposals log is the trail — did the critic help?
//
// The discipline (hybrid-loops): "without an ablation test, the architecture is
// theater." Here the ablation IS the gate of the action.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bytes"
)

// ---- tuning knobs ----

func criticMargin() float64 {
	if v := os.Getenv("CUPEL_CRITIC_MARGIN"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0.05
}

func criticMax() int { return envInt("CUPEL_CRITIC_MAX", 3) }

func criticModel() string {
	if m := os.Getenv("CUPEL_CRITIC_MODEL"); m != "" {
		return m
	}
	return lensModel()
}

// ---- diagnosis (deterministic; the evidence base) ----

// engineDiag is one engine's separation health on the held-out corpus.
type engineDiag struct {
	Name             string
	OwnFloor         float64  // min cosine of the engine's own POS to its prototype (recall floor)
	ConfusionCeiling float64  // max cosine of {NEG ∪ other POS} to its prototype (precision ceiling)
	Margin           float64  // OwnFloor − ConfusionCeiling
	LowExamples      []string // own POS, weakest first (the recall pressure)
	HighConfusers    []string // confusers, strongest first (the precision pressure)
	BadVerdicts      int      // not-useful + mixed live verdicts joined from fires
}

// diagnoseEngines embeds the corpus once and computes each engine's separation
// margin against its prototype. It also returns the corpus vectors so the
// ablation can reuse them without re-embedding. err is non-nil only when the
// local embedder is unreachable (the critic then has no fallback for margins).
func diagnoseEngines(ctx context.Context, engines []engine) ([]engineDiag, map[string][]float64, error) {
	protos, err := loadOrBuildPrototypes(ctx, engines)
	if err != nil {
		return nil, nil, err
	}
	byEngine := probeByEngine()
	negs := probeNegatives()

	// One batch embed of every distinct corpus text.
	var texts []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			texts = append(texts, s)
		}
	}
	for _, ex := range probePositives() {
		add(ex.Text)
	}
	for _, n := range negs {
		add(n)
	}
	// Chunked: a grounded corpus can be hundreds of paragraphs — too many for one
	// embed round trip's budget. Each chunk gets its own timeout (client.go).
	vecs, err := embedTextsBatched(texts, 64)
	if err != nil {
		return nil, nil, err
	}
	corpus := make(map[string][]float64, len(texts))
	for i, t := range texts {
		corpus[t] = vecs[i]
	}

	badVerdicts := engineBadVerdicts()

	var diags []engineDiag
	for i := range engines {
		e := &engines[i]
		proto, ok := protos[e.Name]
		if !ok {
			continue // no prototype (shouldn't happen for confirmed engines)
		}
		own := byEngine[e.Name]
		d := engineDiag{Name: e.Name, OwnFloor: 2, ConfusionCeiling: -2, BadVerdicts: badVerdicts[e.Name]}

		// recall floor + the weakest own examples
		type scored struct {
			text string
			sim  float64
		}
		var owns []scored
		for _, t := range own {
			s := cosine(corpus[t], proto)
			owns = append(owns, scored{t, s})
			if s < d.OwnFloor {
				d.OwnFloor = s
			}
		}
		sort.Slice(owns, func(a, b int) bool { return owns[a].sim < owns[b].sim })
		for k := 0; k < len(owns) && k < 2; k++ {
			d.LowExamples = append(d.LowExamples, fmt.Sprintf("%.3f  %s", owns[k].sim, owns[k].text))
		}

		// precision ceiling + the strongest confusers (negatives + other engines' POS)
		var conf []scored
		for _, n := range negs {
			conf = append(conf, scored{n, cosine(corpus[n], proto)})
		}
		for name, texts := range byEngine {
			if name == e.Name {
				continue
			}
			for _, t := range texts {
				conf = append(conf, scored{t, cosine(corpus[t], proto)})
			}
		}
		for _, c := range conf {
			if c.sim > d.ConfusionCeiling {
				d.ConfusionCeiling = c.sim
			}
		}
		sort.Slice(conf, func(a, b int) bool { return conf[a].sim > conf[b].sim })
		for k := 0; k < len(conf) && k < 2; k++ {
			d.HighConfusers = append(d.HighConfusers, fmt.Sprintf("%.3f  %s", conf[k].sim, conf[k].text))
		}

		d.Margin = d.OwnFloor - d.ConfusionCeiling
		diags = append(diags, d)
	}
	sort.Slice(diags, func(a, b int) bool { return diags[a].Margin < diags[b].Margin })
	return diags, corpus, nil
}

// engineBadVerdicts joins fires.jsonl → fire-tags.jsonl and counts the
// not-useful + mixed verdicts per engine (a live precision complaint). Empty
// until real fires accumulate — wired for when they do.
func engineBadVerdicts() map[string]int {
	dir := cupelDir()
	verdictOf := map[string]string{}
	scanJSONL(filepath.Join(dir, "fire-tags.jsonl"), func(line []byte) {
		var t fireTag
		if json.Unmarshal(line, &t) == nil && t.HookEventID != "" {
			verdictOf[t.HookEventID] = t.Verdict
		}
	})
	out := map[string]int{}
	scanJSONL(filepath.Join(dir, "fires.jsonl"), func(line []byte) {
		var f fire
		if json.Unmarshal(line, &f) != nil {
			return
		}
		if v := verdictOf[f.HookEventID]; v == "not-useful" || v == "mixed" {
			out[f.Engine]++
		}
	})
	return out
}

// ---- the LLM proposal (the reasoner half) ----

type criticProposal struct {
	Prototype string `json:"prototype"`
	Rationale string `json:"rationale"`
}

// proposePrototype asks the critic model for a tighter prototype for one engine,
// given its current prototype and the weak evidence. Cached on disk by content
// hash (the cost rule): identical engine+prototype+evidence ⇒ zero API spend.
func proposePrototype(ctx context.Context, key string, e *engine, d engineDiag) (criticProposal, bool, error) {
	system := `You sharpen the reference text ("prototype") of a wish-fulfillment engine's RECRUITMENT FACE for an embedding-similarity gate. The prototype is embedded once; a prompt trips the gate when its embedding is close to the prototype. Your job: rewrite the prototype so genuine recruitment prose for THIS engine sits closer to it, while benign text and OTHER engines' recruitment prose sit further. Keep it 30-50 words, in the recruitment register (as if recruiting), concrete and vivid. Do NOT quote or paraphrase the example sentences you are shown — they are held-out test cases. Respond ONLY with strict JSON: {"prototype": "<the rewritten prototype>", "rationale": "<=15 words"}.`

	var b strings.Builder
	fmt.Fprintf(&b, "Engine: %s\nRecruitment face: %s\nGrants (slot 3): %s\nSkips (slot 2): %s\n\n", e.Name, e.Face, e.Slot3, e.Slot2)
	fmt.Fprintf(&b, "Current prototype:\n%s\n\n", e.Prototype)
	fmt.Fprintf(&b, "Separation now: own recall floor %.3f, confusion ceiling %.3f, margin %+.3f (higher margin is better).\n\n", d.OwnFloor, d.ConfusionCeiling, d.Margin)
	if len(d.LowExamples) > 0 {
		b.WriteString("This engine's own recruitment examples that score LOWEST (should score high — pull these closer):\n")
		for _, x := range d.LowExamples {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
	}
	if len(d.HighConfusers) > 0 {
		b.WriteString("Confusers that score HIGHEST against this prototype (benign or other-engine — push these away):\n")
		for _, x := range d.HighConfusers {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
	}
	if d.BadVerdicts > 0 {
		fmt.Fprintf(&b, "\nLive feedback: %d fire(s) on this engine were tagged not-useful/mixed — the face may be over-reaching.\n", d.BadVerdicts)
	}
	prompt := b.String()

	cf := criticCachePath(criticModel(), e.Name, e.Prototype, prompt)
	if raw, rerr := os.ReadFile(cf); rerr == nil {
		var p criticProposal
		if json.Unmarshal(raw, &p) == nil && p.Prototype != "" {
			return p, true, nil
		}
	}
	p, err := callCritic(ctx, key, system, prompt)
	if err != nil {
		return p, false, err
	}
	if raw, merr := json.Marshal(p); merr == nil {
		_ = os.WriteFile(cf, raw, 0o600)
	}
	return p, false, nil
}

func criticCachePath(model, engineName, currentProto, evidence string) string {
	dir := filepath.Join(cupelDir(), "critic-cache")
	_ = os.MkdirAll(dir, 0o700)
	sum := sha256.Sum256([]byte(model + "\x00" + engineName + "\x00" + currentProto + "\x00" + evidence))
	return filepath.Join(dir, hex.EncodeToString(sum[:])[:24]+".json")
}

// callCritic mirrors callLens (client.go) — same raw net/http, no-retry, tight
// timeout — but parses the critic's {prototype, rationale} schema. client.go is
// left untouched (its callLens has a different output shape).
func callCritic(ctx context.Context, key, system, prompt string) (criticProposal, error) {
	var out criticProposal
	if key == "" {
		return out, fmt.Errorf("no api key")
	}
	body, err := json.Marshal(msgReq{
		Model: criticModel(), MaxTokens: 400, System: system,
		Messages: []msgContent{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return out, fmt.Errorf("anthropic critic: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var mr msgResp
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return out, err
	}
	var text string
	for _, b := range mr.Content {
		if b.Type == "text" {
			text = b.Text
			break
		}
	}
	if i, j := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}'); i >= 0 && j > i {
		text = text[i : j+1]
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return out, fmt.Errorf("anthropic: unparseable proposal: %w", err)
	}
	return out, nil
}

// ---- the ablation (the gate of the action) ----

type ablation struct {
	Accepted      bool
	Reason        string
	MarginAfter   float64
	OwnFloorAfter float64
	CeilingAfter  float64
}

// ablateProposal embeds the proposed prototype and applies the 3-part criterion
// against the same held-out corpus vectors: (1) margin strictly improves; (2) no
// benign NEG crosses the gate threshold; (3) own recall floor does not regress.
func ablateProposal(ctx context.Context, engineName, newProto string, corpus map[string][]float64, base engineDiag) (ablation, error) {
	var ab ablation
	pv, err := embedTexts(ctx, []string{newProto})
	if err != nil {
		return ab, err
	}
	v := pv[0]
	byEngine := probeByEngine()
	negs := probeNegatives()

	ownFloor := 2.0
	for _, t := range byEngine[engineName] {
		if s := cosine(corpus[t], v); s < ownFloor {
			ownFloor = s
		}
	}
	ceiling := -2.0
	negOverGate := ""
	for _, n := range negs {
		s := cosine(corpus[n], v)
		if s > ceiling {
			ceiling = s
		}
		if s > gateThreshold() {
			negOverGate = n
		}
	}
	for name, texts := range byEngine {
		if name == engineName {
			continue
		}
		for _, t := range texts {
			if s := cosine(corpus[t], v); s > ceiling {
				ceiling = s
			}
		}
	}
	ab.OwnFloorAfter, ab.CeilingAfter = ownFloor, ceiling
	ab.MarginAfter = ownFloor - ceiling

	switch {
	case ab.MarginAfter <= base.Margin:
		ab.Reason = fmt.Sprintf("margin did not improve (%.3f → %.3f)", base.Margin, ab.MarginAfter)
	case negOverGate != "":
		ab.Reason = fmt.Sprintf("a benign example would cross the gate (%.3f > %.3f): %q", cosine(corpus[negOverGate], v), gateThreshold(), snippet(negOverGate, 60))
	case ownFloor < base.OwnFloor:
		ab.Reason = fmt.Sprintf("own recall floor regressed (%.3f → %.3f)", base.OwnFloor, ownFloor)
	default:
		ab.Accepted = true
		ab.Reason = fmt.Sprintf("margin %+.3f → %+.3f", base.Margin, ab.MarginAfter)
	}
	return ab, nil
}

// ---- the action (write proposals; optionally a candidate substrate) ----

type proposalRecord struct {
	Ts              string   `json:"ts"`
	Engine          string   `json:"engine"`
	Status          string   `json:"status"` // accepted | rejected
	Reason          string   `json:"reason"`
	BeforePrototype string   `json:"before_prototype"`
	AfterPrototype  string   `json:"after_prototype"`
	MarginBefore    float64  `json:"margin_before"`
	MarginAfter     float64  `json:"margin_after"`
	MarginDelta     float64  `json:"margin_delta"`
	OwnFloorBefore  float64  `json:"own_floor_before"`
	OwnFloorAfter   float64  `json:"own_floor_after"`
	Rationale       string   `json:"rationale"`
	LowExamples     []string `json:"low_examples,omitempty"`
	HighConfusers   []string `json:"high_confusers,omitempty"`
	Model           string   `json:"model"`
	SchemaVersion   string   `json:"schema_version"`
}

func proposalsDir() string {
	d := filepath.Join(cupelDir(), "critic-proposals")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func safeEngineName(s string) string {
	return strings.NewReplacer("/", "-", " ", "-").Replace(s)
}

// countCriticProposals counts proposal records (doctor surfaces this to close
// the loop visibly). The candidate substrate is not a proposal record.
func countCriticProposals() int {
	ents, err := os.ReadDir(filepath.Join(cupelDir(), "critic-proposals"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && e.Name() != "engines.candidate.json" {
			n++
		}
	}
	return n
}

func writeProposal(rec proposalRecord) string {
	path := filepath.Join(proposalsDir(), fmt.Sprintf("%s-%s.json",
		time.Now().UTC().Format("20060102T150405"), safeEngineName(rec.Engine)))
	if b, err := json.MarshalIndent(rec, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o600)
	}
	return path
}

// ---- the subcommand ----

func runCritic(args []string) {
	margin := criticMargin()
	max := criticMax()
	apply := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--apply":
			apply = true
		case "--dry":
			apply = false
		case "--max":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					max = n
				}
				i++
			}
		case "--margin":
			if i+1 < len(args) {
				if f, err := strconv.ParseFloat(args[i+1], 64); err == nil {
					margin = f
				}
				i++
			}
		}
	}

	sub, err := loadSubstrate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cupel critic: bad engines.json:", err)
		os.Exit(1)
	}

	// Per-operation contexts: the critic is a dev-time tool (not a latency-bound
	// hook), so each embed/proposal gets its own generous timeout rather than one
	// shared budget that a slow LLM call would exhaust mid-loop.
	dctx, dcancel := context.WithTimeout(context.Background(), httpTimeout()*4)
	diags, corpus, err := diagnoseEngines(dctx, sub.Engines)
	dcancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cupel critic: embedder unreachable (%s @ %s): %v\n", embedModel(), ollamaURL(), err)
		fmt.Fprintln(os.Stderr, "the critic has no fallback for margin diagnostics — start ollama and retry.")
		return // non-fatal, writes nothing
	}

	// margin table (weakest first)
	fmt.Println("cupel critic — prototype separation diagnostic")
	fmt.Println(strings.Repeat("─", 46))
	fmt.Printf("%-22s %9s %10s %8s %8s\n", "engine", "own_floor", "confuse", "margin", "verdicts")
	for _, d := range diags {
		v := "-"
		if d.BadVerdicts > 0 {
			v = fmt.Sprintf("%d bad", d.BadVerdicts)
		}
		fmt.Printf("%-22s %9.3f %10.3f %+8.3f %8s\n", d.Name, d.OwnFloor, d.ConfusionCeiling, d.Margin, v)
	}
	fmt.Printf("gate threshold: %.2f   margin flag: <%.3f   corpus: %d POS / %d NEG\n",
		gateThreshold(), margin, len(probePositives()), len(probeNegatives()))

	// flag the weak engines
	var flagged []engineDiag
	for _, d := range diags {
		if d.Margin < margin || d.BadVerdicts > 0 {
			flagged = append(flagged, d)
		}
	}
	if max < 0 {
		max = 0 // guard: --max -1 / CUPEL_CRITIC_MAX=-1 would panic on flagged[:max]
	}
	if len(flagged) > max {
		flagged = flagged[:max] // diags already sorted weakest-first
	}
	if len(flagged) == 0 {
		fmt.Println("\nall engine margins healthy — nothing to propose.")
		return
	}
	fmt.Printf("\nflagged (weakest %d): %s\n", len(flagged), strings.Join(diagNames(flagged), ", "))

	key := resolveAPIKey()
	if key == "" || os.Getenv("CUPEL_LENS_DISABLED") == "1" {
		fmt.Println("diagnose-only: no API key (or CUPEL_LENS_DISABLED=1) — no proposals generated.")
		return
	}

	byName := map[string]*engine{}
	for i := range sub.Engines {
		byName[sub.Engines[i].Name] = &sub.Engines[i]
	}

	accepted := map[string]string{} // engine → new prototype
	for _, d := range flagged {
		e := byName[d.Name]
		if e == nil {
			continue
		}
		pctx, pcancel := context.WithTimeout(context.Background(), httpTimeout()*8)
		prop, cached, perr := proposePrototype(pctx, key, e, d)
		pcancel()
		if perr != nil {
			fmt.Printf("  %-22s proposal error: %v\n", d.Name, perr)
			continue
		}
		actx, acancel := context.WithTimeout(context.Background(), httpTimeout()*4)
		ab, aerr := ablateProposal(actx, d.Name, prop.Prototype, corpus, d)
		acancel()
		if aerr != nil {
			fmt.Printf("  %-22s ablation error: %v\n", d.Name, aerr)
			continue
		}
		status := "rejected"
		if ab.Accepted {
			status = "accepted"
			accepted[d.Name] = prop.Prototype
		}
		path := writeProposal(proposalRecord{
			Ts: nowTs(), Engine: d.Name, Status: status, Reason: ab.Reason,
			BeforePrototype: e.Prototype, AfterPrototype: prop.Prototype,
			MarginBefore: d.Margin, MarginAfter: ab.MarginAfter, MarginDelta: ab.MarginAfter - d.Margin,
			OwnFloorBefore: d.OwnFloor, OwnFloorAfter: ab.OwnFloorAfter,
			Rationale: prop.Rationale, LowExamples: d.LowExamples, HighConfusers: d.HighConfusers,
			Model: criticModel(), SchemaVersion: sub.SchemaVersion,
		})
		cacheNote := ""
		if cached {
			cacheNote = " (cached)"
		}
		fmt.Printf("  %-22s %-8s %s%s\n      → %s\n", d.Name, status, ab.Reason, cacheNote, filepath.Base(path))
	}

	if apply && len(accepted) > 0 {
		for i := range sub.Engines {
			if np, ok := accepted[sub.Engines[i].Name]; ok {
				sub.Engines[i].Prototype = np
			}
		}
		cand := filepath.Join(proposalsDir(), "engines.candidate.json")
		if b, err := json.MarshalIndent(sub, "", "  "); err == nil {
			_ = os.WriteFile(cand, b, 0o600)
			fmt.Printf("\n--apply: %d accepted edit(s) merged into %s\n", len(accepted), cand)
			fmt.Println("review the diff, then move it over cmd/cupel/engines.json yourself (the substrate stays human-gated).")
		}
	} else if len(accepted) > 0 {
		fmt.Printf("\n%d edit(s) accepted by ablation. Re-run with --apply to emit a candidate engines.json to diff.\n", len(accepted))
	}
	fmt.Printf("proposals written to %s\n", proposalsDir())
}

func diagNames(ds []engineDiag) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}
