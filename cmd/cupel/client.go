package main

// client.go — the two HTTP surfaces the v2 hybrid lens needs:
//
//   1. ollama /api/embed  — the cheap local GATE (runs every prompt).
//   2. Anthropic /v1/messages (Haiku) — the LENS (runs only past the gate).
//
// Both are raw net/http on purpose. cupel is a UserPromptSubmit hook that runs
// on every turn, so it wants zero third-party deps and ONE tight timeout with
// NO retries (a hook must never delay a turn). This mirrors lexicon's
// render/internal/client *pattern* — key from ANTHROPIC_API_KEY, Haiku as the
// lens model, fail-safe when the key is absent — without importing the SDK
// (lexicon's client is a render CLI with a different latency/retry profile).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LensModel is the fast/cheap per-fire model, matching lexicon's LensModel.
// Overridable via CUPEL_LENS_MODEL.
const LensModel = "claude-haiku-4-5-20251001"

// ---- key resolution (env first, then a .env fallback for local runs) ----

// resolveAPIKey returns the Anthropic key, or "" if none is reachable (the
// lens then stays disabled — fail-safe). Order: the live process env wins
// (the way a Claude Code session or lexicon would have it); then CUPEL_ENV_FILE
// if set; then ~/.claude/cupel/.env. The repo's gitignored .env is reachable by
// pointing CUPEL_ENV_FILE at it.
func resolveAPIKey() string {
	if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
		return k
	}
	if p := os.Getenv("CUPEL_ENV_FILE"); p != "" {
		loadDotEnv(p)
	} else {
		loadDotEnv(filepath.Join(cupelDir(), ".env"))
	}
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

// loadDotEnv reads KEY=VALUE lines into the process env if not already set
// (lexicon's tiny loader — no godotenv dep for this trivial format).
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

// ---- ollama embedding (the gate) ----

func ollamaURL() string {
	if u := os.Getenv("CUPEL_OLLAMA_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:11434"
}

func embedModel() string {
	if m := os.Getenv("CUPEL_EMBED_MODEL"); m != "" {
		return m
	}
	// nomic-embed-text: the separation probe re-run on every local embedder
	// picked this one — clean class separation (POS floor 0.589 > NEG ceiling
	// 0.556) at ~60ms warm. qwen2:1.5b separated wider (+0.138) but cost ~0.9s
	// on every prompt; all-minilm was 20ms but OVERLAPPED (-0.072). nomic is the
	// fast-and-clean middle.
	return "nomic-embed-text"
}

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}
type embedResp struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// embedTexts returns one L2-normalized vector per input (normalized so cosine
// is a plain dot product downstream). A single round trip; ollama batches.
func embedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	body, err := json.Marshal(embedReq{Model: embedModel(), Input: texts})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL()+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: status %d", resp.StatusCode)
	}
	var er embedResp
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	if len(er.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d vectors for %d inputs", len(er.Embeddings), len(texts))
	}
	for i := range er.Embeddings {
		normalize(er.Embeddings[i])
	}
	return er.Embeddings, nil
}

// embedTextsBatched embeds many texts in fixed-size chunks, each round trip on
// its own generous timeout, so a large corpus can never exceed a single request
// budget. embedTexts sends all inputs in one POST (fine for the per-prompt gate's
// one or few short texts); the dev-time critic and the recall test embed hundreds
// of long held-out paragraphs — and a long paragraph embeds an order of magnitude
// slower than a short prompt (~0.6s vs ~60ms on nomic), so the hook's tight
// httpTimeout would blow on a single chunk. This path is dev-time, not the
// latency-bound hook, so each chunk gets a flat 90s (proven on the recall corpus).
func embedTextsBatched(texts []string, chunk int) ([][]float64, error) {
	if chunk <= 0 {
		chunk = 64
	}
	all := make([][]float64, 0, len(texts))
	for i := 0; i < len(texts); i += chunk {
		end := i + chunk
		if end > len(texts) {
			end = len(texts)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		vs, err := embedTexts(ctx, texts[i:end])
		cancel()
		if err != nil {
			return nil, fmt.Errorf("embed chunk %d-%d: %w", i, end, err)
		}
		all = append(all, vs...)
	}
	return all, nil
}

func normalize(v []float64) {
	var n float64
	for _, x := range v {
		n += x * x
	}
	n = math.Sqrt(n)
	if n == 0 {
		return
	}
	for i := range v {
		v[i] /= n
	}
}

// cosine of two already-normalized vectors (plain dot product).
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return -1
	}
	var dot float64
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// ---- Anthropic Haiku (the lens) ----

type lensVerdict struct {
	Fires      bool   `json:"fires"`
	Engine     string `json:"engine"`
	Confidence string `json:"confidence"`
	Why        string `json:"why"`
}

type msgReq struct {
	Model      string         `json:"model"`
	MaxTokens  int            `json:"max_tokens"`
	System     string         `json:"system"`
	Messages   []msgContent   `json:"messages"`
	Tools      []toolDef      `json:"tools,omitempty"`
	ToolChoice *toolChoiceDef `json:"tool_choice,omitempty"`
}
type msgContent struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
type toolChoiceDef struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}
type msgRespBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}
type msgResp struct {
	Content []msgRespBlock `json:"content"`
}

// lensVerdictTool defines the structured-output tool the lens forces Haiku to
// call. Tool-use guarantees the response matches the schema — no more parsing
// free-form JSON out of narrative prose. The historical text-only path (and
// parseLensResponse) is kept as a fallback for any deployment where the model
// doesn't return a tool_use block.
var lensVerdictTool = toolDef{
	Name:        "verdict",
	Description: "Record whether the prompt is actively running a recruitment face and, if so, which one.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"fires": map[string]any{
				"type":        "boolean",
				"description": "true only if the prompt is actively running (not merely discussing or asking about) a recruitment face.",
			},
			"engine": map[string]any{
				"type":        []string{"string", "null"},
				"description": "When fires=true, the single dominant engine name copied verbatim from the system prompt's list. Null otherwise.",
			},
			"confidence": map[string]any{
				"type": "string",
				"enum": []string{"low", "medium", "high"},
			},
			"why": map[string]any{
				"type":        "string",
				"description": "At most 12 words explaining the verdict.",
			},
		},
		"required": []string{"fires"},
	},
}

func lensModel() string {
	if m := os.Getenv("CUPEL_LENS_MODEL"); m != "" {
		return m
	}
	return LensModel
}

// callLens asks Haiku to adjudicate the prompt against the engine faces and
// returns its strict-JSON verdict. The caller supplies the system prompt and
// is responsible for the on-disk cache (cost rule) — this is the raw call.
func callLens(ctx context.Context, key, system, prompt string) (lensVerdict, error) {
	var out lensVerdict
	if key == "" {
		return out, errors.New("no api key")
	}
	body, err := json.Marshal(msgReq{
		Model: lensModel(), MaxTokens: 200, System: system,
		Messages:   []msgContent{{Role: "user", Content: prompt}},
		Tools:      []toolDef{lensVerdictTool},
		ToolChoice: &toolChoiceDef{Type: "tool", Name: "verdict"},
	})
	if err != nil {
		return out, fmt.Errorf("anthropic: marshal request: %w", err)
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
	// Structured-output path: the request forces a tool_use call whose input
	// matches the verdict schema. This is the canonical path; the parseLens-
	// Response fallback below covers older deployments or unexpected shapes.
	for _, b := range mr.Content {
		if b.Type == "tool_use" && b.Name == "verdict" && len(b.Input) > 0 {
			var v lensVerdict
			if json.Unmarshal(b.Input, &v) == nil {
				return v, nil
			}
		}
	}
	// Free-form text fallback (handles models that ignore tool_choice or any
	// deployment we mis-configure). Same safe-default semantics as the rest of
	// the parser: prose-only → no fire.
	var text string
	for _, b := range mr.Content {
		if b.Type == "text" {
			text = b.Text
			break
		}
	}
	return parseLensResponse(text), nil
}

// parseLensResponse extracts the verdict from the model's reply. The system
// prompt asks for strict JSON; newer Haiku revisions sometimes narrate instead
// when the input is clearly benign ("I don't see this prompt running a
// recruitment face — it's a routine question…"). Prose-only is treated as
// {fires: false} — the safe default for a smoke detector. Only an explicit
// fires=true verdict fires. Genuine transport failures stay as errors and are
// handled by the caller's fail-safe.
func parseLensResponse(text string) lensVerdict {
	var out lensVerdict
	// Strict JSON first.
	if json.Unmarshal([]byte(text), &out) == nil {
		return out
	}
	// JSON-in-prose: slice to the outermost braces.
	if i, j := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}'); i >= 0 && j > i {
		if json.Unmarshal([]byte(text[i:j+1]), &out) == nil {
			return out
		}
	}
	// Prose-only: safe default.
	return lensVerdict{Fires: false, Why: "no parseable verdict (prose-only)"}
}

// httpTimeout bounds BOTH the gate and the lens. A hook must never hang a turn;
// on timeout we fail open (gate) or fail safe (lens skipped).
func httpTimeout() time.Duration {
	return time.Duration(envInt("CUPEL_HTTP_TIMEOUT_MS", 8000)) * time.Millisecond
}
