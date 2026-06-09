package main

// gate.go — the v2 GATE: the cheap, every-prompt layer. It embeds the prompt
// with a local ollama model and asks whether the prompt is within
// CUPEL_GATE_THRESHOLD cosine of any engine's recruitment-register prototype.
// Tuned RECALL-FIRST: a false trip only costs one Haiku lens call (which then
// rejects it), so the gate errs toward letting smoke through. The held-out
// finding that justifies this whole layer: v1's lexical match fired on 0 of
// 2,656 paragraphs of real recruitment-ancestor prose — verbatim cliché
// matching has near-zero real recall; recruitment register is semantic.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// gateThreshold is the cosine cutoff. Default 0.57 sits between the held-out
// probe's POS floor (0.589) and NEG ceiling (0.556) for nomic-embed-text —
// recall-first with the lens as the precision backstop. Tune from doctor's
// gate_sim distribution once real fires accumulate.
func gateThreshold() float64 {
	if v := os.Getenv("CUPEL_GATE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0.57
}

// protoCache is the on-disk cache of prototype vectors. Key binds the embed
// model AND a hash of the prototype texts, so any change to either silently
// invalidates and forces a recompute.
type protoCache struct {
	Key     string               `json:"key"`
	Vectors map[string][]float64 `json:"vectors"`
}

func protoCachePath() string {
	return filepath.Join(cupelDir(), "prototypes.json")
}

// prototypeKey binds model + the concatenated prototype texts.
func prototypeKey(engines []engine) string {
	var buf []byte
	for i := range engines {
		buf = append(buf, engines[i].Name...)
		buf = append(buf, 0)
		buf = append(buf, engines[i].Prototype...)
		buf = append(buf, 0)
	}
	return embedModel() + ":" + shortHash(string(buf))
}

// loadOrBuildPrototypes returns the per-engine prototype vectors, embedding and
// caching them on first use (or after a model/prototype change). One ollama
// round trip on a cache miss; zero on a hit.
func loadOrBuildPrototypes(ctx context.Context, engines []engine) (map[string][]float64, error) {
	key := prototypeKey(engines)
	if b, err := os.ReadFile(protoCachePath()); err == nil {
		var pc protoCache
		if json.Unmarshal(b, &pc) == nil && pc.Key == key && len(pc.Vectors) > 0 {
			return pc.Vectors, nil
		}
	}
	// Cache miss: embed every non-empty prototype in one batch.
	var names, texts []string
	for i := range engines {
		if engines[i].Prototype == "" {
			continue
		}
		names = append(names, engines[i].Name)
		texts = append(texts, engines[i].Prototype)
	}
	vecs, err := embedTexts(ctx, texts)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]float64, len(names))
	for i, n := range names {
		out[n] = vecs[i]
	}
	if b, err := json.Marshal(protoCache{Key: key, Vectors: out}); err == nil {
		_ = os.WriteFile(protoCachePath(), b, 0o600)
	}
	return out, nil
}

// gateScore embeds the prompt and returns the closest engine by cosine to its
// prototype, plus that similarity. err is non-nil only when the local embedder
// is unreachable — the caller then falls back to the v1 lexical score().
func gateScore(ctx context.Context, prompt string, engines []engine) (*engine, float64, error) {
	protos, err := loadOrBuildPrototypes(ctx, engines)
	if err != nil {
		return nil, 0, err
	}
	pv, err := embedTexts(ctx, []string{prompt})
	if err != nil {
		return nil, 0, err
	}
	var best *engine
	bestSim := -1.0
	for i := range engines {
		v, ok := protos[engines[i].Name]
		if !ok {
			continue
		}
		if s := cosine(pv[0], v); s > bestSim {
			best, bestSim = &engines[i], s
		}
	}
	return best, bestSim, nil
}
