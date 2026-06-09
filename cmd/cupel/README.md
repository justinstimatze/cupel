# cupel hook — the dual-use smoke detector (v2 hybrid)

> **For readers arriving here from the catalog:** this doc covers cupel's
> *optional runtime layer* — a [Claude Code](https://claude.com/claude-code)
> `UserPromptSubmit` hook that watches your live prompts and fires when the
> language is running an engine's counterfeit. The catalog of engines and
> dossiers (the project's main artifact) doesn't need any of this — see the
> top-level `README.md`. Skip this file if you're not running Claude Code.

cupel's runtime layer (the "running shape"). A Claude Code `UserPromptSubmit` hook
that watches the current prompt and fires a terse observation **only when the
content is running the **counterfeit** of a confirmed engine — silent
otherwise. It's a smoke detector, not an engine narrator: it stays quiet until a
recruitment pattern crosses the threshold, so it adds signal without piling onto
any other ambient Claude Code hooks the user has installed.

## Why v2 (the held-out finding)

v1 was a **lexical** matcher: count how many of each engine's authored `face_terms`
appear in the prompt. A held-out generalization test killed it — v1 fired on **0 of
2,656 paragraphs** of real recruitment-ancestor prose (Robison, the Kybalion, Trine,
Sumner, Ovid, Pericles). Its `face_terms` only match verbatim *modern clichés*, so
the green 10/10 unit suite was self-fulfilling (the positives were written around the
keywords). **Near-zero real recall.** Recruitment register is *semantic*, not lexical.

v2 is a **hybrid loop** (per the `hybrid-loops` skill) with the lens split into a
cheap recall layer and an expensive precision layer:

- **Substrate-as-vocabulary** — `engines.json`: each engine carries a `prototype`
  (the recruitment-register reference text the gate embeds) and `face_terms` (kept
  for the lexical fallback).
- **Gate (cheap, every prompt)** — embed the prompt locally (ollama
  `nomic-embed-text`), cosine vs the cached prototype vectors; trip if the closest
  engine is within `CUPEL_GATE_THRESHOLD`. Tuned **recall-first** (a false trip only
  costs one lens call). Validated separation on a held-out set: POS floor 0.589 > NEG
  ceiling 0.556 at ~60ms warm.
- **Lens (expensive, only past the gate)** — Haiku adjudicates *running* a face vs
  merely *discussing* one (the academic/journalistic false-positive the gate can't
  tell apart) and **fixes attribution**. Validated 8/8 on exactly those cases.
- **Gate (restraint)** — global cooldown keeps it sparse.
- **Action** — one injected line. **Calibration** — `fires.jsonl` + `metrics.jsonl`.

## Build & install

```sh
ollama pull nomic-embed-text          # the gate's embedder (one time)
go build -o ~/go/bin/cupel ./cmd/cupel
```

Wire it globally (fires in every session) in `~/.claude/settings.json`, alongside
the existing UserPromptSubmit hooks:

```json
{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"cupel hook"}]}]}}
```

The **API key** for the lens is read from the live process env (`ANTHROPIC_API_KEY`)
first; if it isn't there, set `CUPEL_ENV_FILE` to a `.env` (the hook runs globally and
can't find the repo `.env` by a relative path), else it looks in
`~/.claude/cupel/.env`. **No key is fine** — the lens is fail-safe (gate-only).

## How it fires (gate → cooldown → lens)

1. **Gate.** Embed the prompt; cosine vs cached engine prototypes. Below
   `CUPEL_GATE_THRESHOLD` → silent (the common case). *If the local embedder is
   unreachable, degrade to the v1 lexical match* (`face_terms`, `CUPEL_HOOK_THRESHOLD`).
2. **Cooldown.** Checked *before* the lens, so a silenced turn never spends an API call.
3. **Lens.** Haiku returns `{fires, engine, confidence, why}`. `fires:false` → silent
   (precision rejection: it's discussion/benign). `fires:true` → fire, using the lens's
   engine attribution and `why`.
4. **Action.** One line naming the engine, its counterfeit, slot-3 granted / slot-2
   skipped, the lens's `why` (or matched terms in fallback), and the value-flow question.

**Fail-safe matrix** (every path swallows errors and never blocks a turn):

| ollama | key | behaviour |
|---|---|---|
| up | present | full stack — gate recall, lens precision + attribution |
| up | absent / `CUPEL_LENS_DISABLED=1` | gate-only — fire on the embedding match, no API call |
| down | — | lexical fallback — v1 `face_terms` match (degraded recall) |

## Tuning (env)

| var | default | effect |
|---|---|---|
| `CUPEL_GATE_THRESHOLD=<f>` | 0.57 | embedding-gate cosine cutoff (recall-first; re-tune from `doctor`) |
| `CUPEL_EMBED_MODEL=<name>` | nomic-embed-text | ollama embedder (a different model needs its own threshold) |
| `CUPEL_OLLAMA_URL=<url>` | http://localhost:11434 | ollama base url |
| `CUPEL_LENS_MODEL=<id>` | claude-haiku-4-5-20251001 | the per-fire lens model |
| `CUPEL_LENS_DISABLED=1` | — | force gate-only (no API calls) without removing the key |
| `CUPEL_ENV_FILE=<path>` | ~/.claude/cupel/.env | `.env` to source the key from |
| `CUPEL_HTTP_TIMEOUT_MS=<n>` | 8000 | gate + lens timeout (a hook must not hang) |
| `CUPEL_HOOK_COOLDOWN=<sec>` | 900 | seconds between fires |
| `CUPEL_HOOK_THRESHOLD=<n>` | 2 | **lexical-fallback only:** min distinct face-term hits |
| `CUPEL_ENGINES=<path>` | embedded | load `engines.json` from disk — tune without recompiling |
| `CUPEL_SKIP=1` / `CUPEL_METRICS_DISABLED=1` | — | short-circuit / stop writing metrics |

(House-standard hook pattern: defensive total-swallow, `SKIP` env, snippet-only
prompt capture, `~/.claude/cupel/` at 0700/0600, a per-invocation metrics log, a
disk override for live tuning, and Haiku as the `LensModel`. Cupel's cheap layer is
an *embedding* gate, not lexical — because recruitment register is semantic, where
lexical had near-zero recall.)

## Calibration & ablation (the discipline that keeps it honest)

Every **invocation** appends to `metrics.jsonl` (fired?, gated-reason, `gate_mode`,
`gate_sim`, `lens_ms`, latency) — so fire-rate (sparsity), gate behaviour, and lens
spend are all measurable. Every **fire** appends to `fires.jsonl` (`engine`, `face`,
`gate_mode`, `gate_sim`, `lens_verdict`, `lens_why`, `prompt_snippet`, `hook_event_id`).
`hook.log` carries human one-liners; `state.json` the cooldown; `fire-tags.jsonl` the
verdicts; `prototypes.json` the cached gate vectors (rebuilt on any model/prototype change).

- **`cupel doctor`** — rolls it up: fire-rate (warns >~10%), gate mode split, lens call
  count + avg latency, why silent runs were gated (`below-gate` / `lens-rejected` / …),
  fires by engine, verdict tally, and recent fires with `sim`/lens-verdict + `hook_event_id`.
- **`cupel mark-fire <id> <useful|mixed|not-useful>`** — tag a fire. The hit-rate is what
  hardens the engine set from live material instead of by guesswork.
- **`go test ./cmd/cupel`** — the ablation harness, split by layer and **skipped when a
  dep is absent** (so it's green offline):
  - `hook_test.go` — the lexical *fallback*: each face fires (≥2 hits), benign stays
    silent, stripping `face_terms` drops the fire.
  - `gate_test.go` *(needs ollama)* — **separation**: on a held-out set, POS_min > NEG_max
    and the default threshold sits in the gap. This is the gate's reason to exist over v1.
  - `lens_test.go` *(needs a key; cached)* — fires on running-recruitment, **silent on
    academic discussion**, and corrects a known gate mis-attribution.
  - `critic_test.go` *(diagnose/ablate need ollama; proposal needs a key, cached)* — margins
    compute and sort; **the ablation rejects a worse prototype** (the gate of the dev-time loop);
    the full propose→ablate path runs for one engine.
## The stacked loop — `cupel critic` (v2.1, dev-time)

The development-time loop that wraps the runtime (hybrid-loops STACKING): it reads the
runtime's evidence and proposes hardening edits to the substrate — but never mutates it.

- **Evidence** — the held-out separation corpus (`probe.go`: ~2 original recruitment
  examples per engine + benign negatives, the *same* corpus `gate_test` asserts on) joined
  to live `fires.jsonl` + verdicts (a `not-useful`/`mixed` tag is a precision complaint).
- **Diagnose** (deterministic) — per engine, `own_floor` (min cosine of its own examples to
  its prototype) − `confusion_ceiling` (max cosine of benign + *other* engines' examples) =
  **margin**. A low/negative margin = the prototype is confusable.
- **Propose** (Haiku, cached) — for the weakest `--max` engines, the model rewrites the
  prototype given its low-scoring own examples and high-scoring confusers.
- **Ablation (the gate of the action)** — re-embed the proposal; **accept only if** (1) the
  margin strictly improves, (2) no benign example crosses `CUPEL_GATE_THRESHOLD`, and (3) the
  own-recall floor does not regress. This refusal is what separates the loop from theater.
- **Action** — write *every* proposal (accepted *and* rejected, with the margin delta and
  reason) to `~/.claude/cupel/critic-proposals/`. **Propose-only**: the curated `engines.json`
  is never auto-edited. `cupel critic --apply` merges accepted edits into a candidate
  `engines.candidate.json` for you to diff and adopt by hand.

```sh
cupel critic              # diagnose + propose (needs ollama; key → proposals, else diagnose-only)
cupel critic --apply      # also emit a candidate engines.json to diff
cupel critic --max 5 --margin 0.08
```

`cupel doctor` surfaces `open critic proposals: N`. Tuning: `CUPEL_CRITIC_MARGIN` (flag cutoff),
`CUPEL_CRITIC_MAX` (cap per run, bounds API spend), `CUPEL_CRITIC_MODEL`, `CUPEL_CRITIC_CORPUS`.

### v2.2 — grounding the critic on real prose (`cupel build-corpus`)

The built-in corpus is *synthetic* held-out prose, so the critic can overfit prototypes to that
register. **v2.2** grounds it on the **real recruitment-ancestor prose** the v1-recall failure was
measured on (Robison/Kybalion/Trine/Ovid/Sumner/Pericles). `cupel build-corpus` regenerates that
corpus from the local ancestor texts (pure text — no ollama or key):

```sh
cupel build-corpus --dir /path/to/ancestor-texts --out /path/to/corpus.json   # real POS per engine + benign NEG
CUPEL_CRITIC_CORPUS=/path/to/corpus.json cupel critic --max 6
```

- **Real-only by default.** Engines with an ancestor text get real POS; the rest are absent (the
  critic leaves them at the `OwnFloor=2` sentinel, never flagged). `--fill-synthetic` falls back to
  the builtin POS for ungrounded engines if you want full coverage.
- **The corpus is derived/ephemeral** — built from the `/tmp` public-domain texts (re-downloaded each
  session), so it is **not committed**; `build-corpus` is the reproducibility artifact. If
  `CUPEL_CRITIC_CORPUS` points at a missing/unparseable file the critic **silently** falls back to the
  synthetic seed — the margin table's `corpus: N POS / M NEG` line tells you which is live.
- **Timeout.** The critic embeds the whole corpus in one batch, so a large corpus (e.g. 50 POS × 6
  engines + NEG ≈ 305) may exceed the default budget; raise `CUPEL_HTTP_TIMEOUT_MS` (it is dev-time).

Real prose is messier than the hand-written seed, so grounded margins run lower (often negative) —
that gap *is* the overfitting the synthetic corpus was hiding. Treat accepted edits as candidates to
review, not ground truth — which is exactly why the loop is propose-only.

**Still blocked (the second half of v2.2):** re-tuning `CUPEL_GATE_THRESHOLD` from `doctor`'s
`gate_sim` distribution needs live v2 fires to accumulate (zero so far — the gate is correctly silent
on ordinary work).
