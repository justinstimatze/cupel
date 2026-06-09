package main

// lens.go — the v2 LENS: the expensive, rare layer that runs ONLY when the gate
// trips. Haiku adjudicates whether the prompt is actively RUNNING a recruitment
// face (fires) versus merely DISCUSSING/ANALYZING/ASKING ABOUT one (silent) —
// the precision edge the embedding gate alone gets wrong (it false-fires on
// academic discussion, and mis-attributes ~2/10). The held-out probe scored
// this lens 8/8 on exactly those failure cases. Every call is cached on disk by
// content hash (the cost rule): identical prompt → zero API spend.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lensSystem builds the system prompt FROM the substrate so the lens and the
// gate never drift apart. The "running vs discussing" instruction is the whole
// point of the layer.
func lensSystem(engines []engine) string {
	var b strings.Builder
	b.WriteString(`You are cupel's dual-use smoke detector. cupel catalogs wish-fulfillment "wish-fulfillment engines"; each has a RECRUITMENT FACE that grants the engine's payoff identity (slot 3) while skipping its costly middle (slot 2). The engines and faces:

`)
	for i := range engines {
		e := &engines[i]
		fmt.Fprintf(&b, "%s — %s. grants \"%s\" while skipping \"%s\".\n",
			e.Name, e.Face, e.Slot3, e.Slot2)
	}
	b.WriteString(`
Given a user prompt, decide whether the text is ACTIVELY RUNNING one of these recruitment faces — i.e. it is itself persuading/recruiting the reader toward the payoff-minus-cost. Crucial distinction: text that merely DISCUSSES, ANALYZES, or ASKS ABOUT recruitment (academic, journalistic, a question) does NOT fire — only text performing the recruitment fires. Ordinary work/benign text does not fire.

Respond ONLY with strict JSON: {"fires": bool, "engine": "<name or null>", "confidence": "low|medium|high", "why": "<=12 words, why it fires or not"}. The "engine" value must be exactly ONE engine name copied verbatim from the list above (or null when it does not fire) — never combine, join, or invent names; if two seem to apply, pick the single dominant one.`)
	return b.String()
}

// runLens calls Haiku (cached) to adjudicate the prompt. cached reports whether
// the verdict came from disk (no API spend). err is non-nil on key-absent or
// transport failure — the caller then decides the fail-safe path.
func runLens(ctx context.Context, key string, engines []engine, prompt string) (v lensVerdict, cached bool, err error) {
	system := lensSystem(engines)
	cf := lensCachePath(lensModel(), system, prompt)
	if b, rerr := os.ReadFile(cf); rerr == nil {
		if json.Unmarshal(b, &v) == nil {
			return v, true, nil
		}
	}
	v, err = callLens(ctx, key, system, prompt)
	if err != nil {
		return v, false, err
	}
	if b, merr := json.Marshal(v); merr == nil {
		_ = os.WriteFile(cf, b, 0o600)
	}
	return v, false, nil
}

func lensCachePath(model, system, prompt string) string {
	dir := filepath.Join(cupelDir(), "lens-cache")
	_ = os.MkdirAll(dir, 0o700)
	sum := sha256.Sum256([]byte(model + "\x00" + system + "\x00" + prompt))
	return filepath.Join(dir, hex.EncodeToString(sum[:])[:24]+".json")
}
