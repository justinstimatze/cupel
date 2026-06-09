package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed engines.json
var enginesJSON []byte

// ---- substrate (the engine vocabulary) ----

type engine struct {
	Name        string   `json:"name"`
	Face        string   `json:"face"`
	Slot3       string   `json:"slot3"`
	Slot2       string   `json:"slot2"`
	Prototype   string   `json:"prototype"` // v2: recruitment-register text the gate embeds
	EngineTerms []string `json:"engine_terms"`
	FaceTerms   []string `json:"face_terms"`
}

type substrate struct {
	SchemaVersion string   `json:"schema_version"`
	Engines       []engine `json:"engines"`
}

// loadSubstrate prefers an on-disk engines.json (CUPEL_ENGINES=/path) so
// signatures can be tuned live without recompiling; falls back to embedded.
func loadSubstrate() (substrate, error) {
	raw := enginesJSON
	if p := os.Getenv("CUPEL_ENGINES"); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			raw = b
		} else {
			logln("CUPEL_ENGINES set but unreadable (" + err.Error() + "); using embedded substrate")
		}
	}
	var sub substrate
	err := json.Unmarshal(raw, &sub)
	return sub, err
}

// ---- hook I/O (Claude Code UserPromptSubmit contract) ----

type hookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	Prompt        string `json:"prompt"`
	HookEventName string `json:"hook_event_name"`
}

// fire is one structured calibration record (predict; verdict joined later
// from fire-tags.jsonl via `cupel mark-fire <hook_event_id> <verdict>`).
type fire struct {
	TS            string   `json:"ts"`
	HookEventID   string   `json:"hook_event_id"`
	SessionID     string   `json:"session_id"`
	PromptHash    string   `json:"prompt_hash"`
	PromptSnippet string   `json:"prompt_snippet"`
	Engine        string   `json:"engine"`
	Face          string   `json:"face"`
	FaceHits      int      `json:"face_hits"`         // lexical-fallback only
	Matched       []string `json:"matched,omitempty"` // lexical-fallback only
	GateMode      string   `json:"gate_mode"`         // embedding | lexical-fallback
	GateSim       float64  `json:"gate_sim,omitempty"`
	LensVerdict   string   `json:"lens_verdict,omitempty"` // fired | skipped(no-key) | error | n/a
	LensWhy       string   `json:"lens_why,omitempty"`
	SchemaVersion string   `json:"schema_version"`
}

// metricsRecord is written for EVERY invocation (fired or not) so fire-rate
// and latency are measurable — the cupel hook's whole value is staying sparse,
// which can only be confirmed by counting silent runs too.
type metricsRecord struct {
	Ts        string  `json:"ts"`
	Fired     bool    `json:"fired"`
	Gated     string  `json:"gated,omitempty"`     // short | below-gate | below-threshold | cooldown | lens-rejected | skip | bad-stdin | bad-engines
	GateMode  string  `json:"gate_mode,omitempty"` // embedding | lexical-fallback
	GateSim   float64 `json:"gate_sim,omitempty"`
	TopEngine string  `json:"top_engine,omitempty"`
	TopHits   int     `json:"top_hits"`  // lexical-fallback only
	Threshold int     `json:"threshold"` // lexical-fallback only
	LensMs    int64   `json:"lens_ms,omitempty"`
	TotalMs   int64   `json:"total_ms"`
}

type hookState struct {
	LastFireUnix int64  `json:"last_fire_unix"`
	LastEngine   string `json:"last_engine"`
}

// runHook is the entry point. It is deliberately total: any failure logs and
// returns cleanly (exit 0, no stdout) so a broken cupel never blocks a turn.
func runHook() {
	rec := metricsRecord{Ts: nowTs(), Threshold: envInt("CUPEL_HOOK_THRESHOLD", 2)}
	wallStart := time.Now()
	// One combined defer: recover-then-write-metrics, so a panic gets logged
	// AND tagged in the metrics record (`cupel doctor` rollup surfaces it as a
	// panic-gated turn) before the record is flushed. Two separate defers would
	// run in LIFO and the metrics write would race ahead of the recover.
	defer func() {
		if r := recover(); r != nil {
			logln(fmt.Sprintf("panic: %v", r))
			if rec.Gated == "" {
				rec.Gated = "panic"
			}
		}
		rec.TotalMs = time.Since(wallStart).Milliseconds()
		writeMetrics(rec)
	}()

	if os.Getenv("CUPEL_SKIP") == "1" {
		rec.Gated = "skip"
		return
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(raw) == 0 {
		rec.Gated = "bad-stdin"
		logln("skip: no stdin")
		return
	}
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		rec.Gated = "bad-stdin"
		logln("skip: bad stdin json: " + err.Error())
		return
	}
	prompt := strings.TrimSpace(in.Prompt)
	if len(prompt) < 24 { // too short to carry a recruitment pattern
		rec.Gated = "short"
		return
	}

	sub, err := loadSubstrate()
	if err != nil {
		rec.Gated = "bad-engines"
		logln("skip: bad engines.json: " + err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout())
	defer cancel()

	// ---- GATE ----
	// The cheap every-prompt layer. Embedding similarity vs cached prototypes;
	// if the local embedder is unreachable, degrade to the v1 lexical match.
	var (
		eng         *engine  // the engine we'll fire on
		gateMode    string   // embedding | lexical-fallback
		sim         float64  // embedding cosine (0 in fallback)
		hits        int      // lexical face-term hits (fallback only)
		matched     []string // lexical terms (fallback only)
		lensVerdict = "n/a"  // n/a (fallback) | skipped | error | fired
		lensWhy     string
		detail      string // mode-specific tail of the fire message
	)

	gEng, gSim, gerr := gateScore(ctx, prompt, sub.Engines)
	if gerr != nil {
		gateMode = "lexical-fallback"
		rec.GateMode = gateMode
		logln("gate: embedder unreachable, lexical fallback: " + gerr.Error())
		le, lh, lm := score(prompt, sub.Engines)
		if le != nil {
			rec.TopEngine, rec.TopHits = le.Name, lh
		}
		if le == nil || lh < rec.Threshold {
			rec.Gated = "below-threshold"
			return
		}
		eng, hits, matched = le, lh, lm
		detail = "Matched: " + strings.Join(quoteAll(matched), ", ") + "."
	} else {
		gateMode = "embedding"
		sim = gSim
		rec.GateMode, rec.GateSim = gateMode, gSim
		if gEng != nil {
			rec.TopEngine = gEng.Name
		}
		if gEng == nil || gSim < gateThreshold() {
			rec.Gated = "below-gate"
			return // the common case — silent by design.
		}
		eng = gEng
	}

	// ---- COOLDOWN (both modes; checked before the lens so we never spend an
	// API call while silenced). ----
	cooldown := int64(envInt("CUPEL_HOOK_COOLDOWN", 900))
	st := loadState()
	if time.Now().Unix()-st.LastFireUnix < cooldown {
		rec.Gated = "cooldown"
		return
	}

	// ---- LENS (embedding mode only) ----
	// Past the gate, Haiku adjudicates running-vs-discussing and fixes
	// attribution. Fail-safe: no key / disabled → fire on the gate alone;
	// transport error → keep the gate's call (smoke already seen); a clean
	// "does not fire" → silence (the precision rejection).
	if gateMode == "embedding" {
		key := resolveAPIKey()
		if key == "" || os.Getenv("CUPEL_LENS_DISABLED") == "1" {
			lensVerdict = "skipped"
			detail = fmt.Sprintf("(gate-only, no API lens; embedding match %.2f.)", sim)
		} else {
			lensStart := time.Now()
			v, _, lerr := runLens(ctx, key, sub.Engines, prompt)
			rec.LensMs = time.Since(lensStart).Milliseconds()
			switch {
			case lerr != nil:
				lensVerdict = "error"
				logln("lens error: " + lerr.Error())
				detail = fmt.Sprintf("(lens unreachable; firing on embedding match %.2f.)", sim)
			case !v.Fires:
				rec.Gated = "lens-rejected"
				return // the lens says discussion/benign, not recruitment.
			default:
				lensVerdict, lensWhy = "fired", v.Why
				if le := engineByName(sub.Engines, v.Engine); le != nil {
					eng = le // the lens corrects the gate's attribution.
				}
				detail = "Lens: " + v.Why
			}
		}
	}

	// ---- FIRE ----
	now := time.Now().Unix()
	eventID := shortHash(in.SessionID + "|" + strconv.FormatInt(time.Now().UnixNano(), 10))
	fmt.Print(render(eng, detail))
	rec.Fired = true
	saveState(hookState{LastFireUnix: now, LastEngine: eng.Name})
	appendJSONL("fires.jsonl", fire{
		TS:            nowTs(),
		HookEventID:   eventID,
		SessionID:     in.SessionID,
		PromptHash:    shortHash(prompt),
		PromptSnippet: snippet(prompt, 120),
		Engine:        eng.Name,
		Face:          eng.Face,
		FaceHits:      hits,
		Matched:       matched,
		GateMode:      gateMode,
		GateSim:       sim,
		LensVerdict:   lensVerdict,
		LensWhy:       lensWhy,
		SchemaVersion: sub.SchemaVersion,
	})
	logln(fmt.Sprintf("fire: %s (%s) mode=%s sim=%.3f lens=%s id=%s",
		eng.Name, eng.Face, gateMode, sim, lensVerdict, eventID))
}

// engineByName resolves the lens's attribution back to a substrate engine
// (case-insensitive). Returns nil if the lens named something unknown — the
// caller then keeps the gate's attribution.
func engineByName(engines []engine, name string) *engine {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	for i := range engines {
		if strings.ToLower(engines[i].Name) == name {
			return &engines[i]
		}
	}
	return nil
}

// score returns the engine whose dual-use FACE vocabulary best matches the
// prompt, the count of distinct face terms hit, and which ones. engine_terms
// are not counted toward firing — the alarm is specifically about the
// counterfeit (recruitment face), not the benign wish merely being present.
func score(prompt string, engines []engine) (*engine, int, []string) {
	low := strings.ToLower(prompt)
	var best *engine
	bestHits := 0
	var bestMatched []string
	for i := range engines {
		e := &engines[i]
		var matched []string
		for _, t := range e.FaceTerms {
			if strings.Contains(low, strings.ToLower(t)) {
				matched = append(matched, t)
			}
		}
		if len(matched) > bestHits {
			best, bestHits, bestMatched = e, len(matched), matched
		}
	}
	return best, bestHits, bestMatched
}

// render builds the fire message. detail is the mode-specific tail (the lens's
// "why", the matched lexical terms, or a gate-only note).
func render(e *engine, detail string) string {
	if detail != "" {
		detail = " " + detail
	}
	return fmt.Sprintf(
		"[cupel] This looks like the **%s** engine's **counterfeit** — *%s*: it grants \"%s\" (slot 3) while skipping \"%s\" (slot 2).%s Value-flow check (subjective): is it *enabling* the real slot-2 work, or *substituting* the badge for it?\n",
		e.Name, e.Face, e.Slot3, e.Slot2, detail,
	)
}

// ---- state, logs, helpers (all under ~/.claude/cupel/, 0700/0600) ----

func cupelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	d := filepath.Join(home, ".claude", "cupel")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func loadState() hookState {
	var st hookState
	if b, err := os.ReadFile(filepath.Join(cupelDir(), "state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(st hookState) {
	if b, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(filepath.Join(cupelDir(), "state.json"), b, 0o600)
	}
}

func appendJSONL(name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fh, err := os.OpenFile(filepath.Join(cupelDir(), name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(b, '\n'))
}

func writeMetrics(rec metricsRecord) {
	if os.Getenv("CUPEL_METRICS_DISABLED") == "1" {
		return
	}
	appendJSONL("metrics.jsonl", rec)
}

func logln(msg string) {
	fh, err := os.OpenFile(filepath.Join(cupelDir(), "hook.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fmt.Fprintf(fh, "%s %s\n", nowTs(), msg)
}

func nowTs() string { return time.Now().UTC().Format(time.RFC3339) }

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) // rune-boundary truncation (never split a multibyte char)
	}
	return s
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "\"" + s + "\""
	}
	return out
}
