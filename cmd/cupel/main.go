// Command cupel is the runtime layer for the cupel wish-fulfillment engine project.
//
// The `hook` subcommand is a Claude Code UserPromptSubmit hook that watches the
// current prompt and fires a terse observation ONLY when the content is running
// a dual-use *counterfeit* (recruitment face) of one of the confirmed engines (the
// smoke-detector design — silent until it matters). v2 is a hybrid loop: a
// cheap local embedding GATE on every prompt, then a Haiku LENS only past the
// gate (see hook.go / gate.go / lens.go). `doctor` and `mark-fire` close the
// calibration loop. Room is left for `verify` (re-check cited quotes) as a
// later subcommand; see entries/_backlog.md.
package main

import (
	"fmt"
	"os"
)

const help = `cupel — wish-fulfillment engine runtime

Usage:
  cupel hook                     Claude Code UserPromptSubmit hook: reads a JSON
                                 envelope from stdin, scores the prompt against the
                                 engines' dual-use recruitment signatures, and emits
                                 an observation only when a face clears the threshold.
                                 Defensive: every error path is swallowed (logged to
                                 ~/.claude/cupel/hook.log) so it never blocks a turn.
  cupel doctor                   Roll up the calibration logs: fire-rate (the sparsity
                                 check), latency, why silent runs were gated, recent
                                 fires, and the verdict tally.
  cupel mark-fire <id> <verdict> Tag a fire (hook_event_id from doctor / fires.jsonl)
                                 useful | mixed | not-useful [--notes "..."].
  cupel critic [--apply]         Dev-time stacked loop: diagnose each engine's
                                 prototype separation on the held-out corpus, then
                                 (with a key) propose tighter prototypes for the
                                 weakest and ABLATE them — accept only edits that
                                 widen separation. Propose-only by default; --apply
                                 emits a candidate engines.json to diff. Never
                                 mutates the curated engines.json. [--max N --margin F]
  cupel build-corpus             v2.2 grounding: regenerate the held-out critic corpus
                                 from the local recruitment-ancestor texts (pg*.txt) and
                                 emit JSON for CUPEL_CRITIC_CORPUS so critic calibrates
                                 on real prose, not the synthetic seed. Pure text (no
                                 ollama/key). [--dir D --out F --max N --fill-synthetic]
  cupel build-data               Emit works.json + engines.json (catalog data shape the
                                 Astro frontend at web/ consumes; uses the same parser the
                                 catalog-reading subcommands do, so the JSON can never
                                 drift). Site rendering itself lives in web/ (Astro + Tailwind
                                 + shadcn) — run pnpm dev in web/ for the live surface.
                                 [--works D --readme F --out D]
  cupel coverage                 Rollup over the cards: works-per-engine (the
                                 under-representation gaps, thinnest first) and the
                                 engines-per-work max-bundle leaderboard. Uses the same
                                 catalog parser as build-data so counts cannot drift.
                                 [--reviews D --readme F --top N]
  cupel quotes-audit             Deterministic scan: flag any cite parenthetical
                                 (one containing entries/<slug>.md) whose non-quoted,
                                 non-structural prose exceeds the threshold. Catches
                                 LLM-paraphrase in citation position (the quotes-only
                                 rule). Exit 1 if violations found — usable as a
                                 pre-commit gate. Pure text. [--dir D | --target F]
                                 [--threshold N --quiet]
  cupel tag-audit                Deterministic scan: every engine-tag line in a
                                 works/*.md must use a confirmed engine name, an
                                 <engine>-antagonist-mode variant, or one of a
                                 small allow-list of non-engine tags (solvents
                                 and refusal-modes). Exit 1 on drift. Keeps the
                                 chip namespace from sprawling. [--dirs D,D]
  cupel clusters-lint            Structural scan of theory/cluster-catalog.md:
                                 every numbered table row must match the cluster
                                 row shape; no duplicate slugs; every "### Name"
                                 heading in the "## Cluster intros" section must
                                 match an actual cluster row. Exit 1 on drift.
  cupel glossary-lint            Structural scan of theory/glossary.md plus
                                 theory/glossary-linkable.txt: no duplicate
                                 entry slugs; every line in glossary-linkable.txt
                                 must match a real entry slug (no stale allow-
                                 list entries). Exit 1 on drift.
  cupel works-lint               Structural scan of works/*.md: every file must
                                 have frontmatter (work, author, backing), a
                                 "## The reading" heading with "**The bead.**"
                                 paragraph, at least one engine bullet, and
                                 (when backing: slot-proven) a "## The evidence"
                                 heading. Catches the silent-drop drift the
                                 best-effort parser would otherwise hide.
                                 [--dir D]
  cupel redaction-audit          Deterministic scan: flag tracked .md prose
                                 matching any rule in the project's redaction
                                 pattern set (loaded from a local config file;
                                 see CUPEL_REDACTION_PATTERNS). No-ops silently
                                 when no patterns are configured. Exit 1 if hits
                                 found. Pure text. [--dir D | --target F --quiet]
  cupel redaction-hook           Claude Code PreToolUse:Write|Edit|MultiEdit
                                 hook: reads the tool envelope from stdin and
                                 exits 2 (block) if the proposed content for a
                                 scoped tracked .md file matches any configured
                                 redaction pattern. Exit 0 otherwise.
  cupel new-work                 Scaffold a new works/<slug>.md with valid front-matter
                                 shape. Auto-quotes values containing ': ' (the YAML
                                 mapping operator). Body template includes a bead
                                 placeholder, engine bullets, and (when --backing
                                 slot-proven) a '## The evidence' heading. After
                                 creation, the author fills the prose + runs db-sync.
                                 [--slug X --work T --author A --backing B --engines C,D
                                 --year Y --medium M --source S --layer L --translator T
                                 --author-note N]
  cupel related-add              Append a related_works / related_theory / pending_refs
                                 bullet to a dossier's front-matter. Auto-quotes the
                                 'slug :: gloss' bullet form (which real YAML otherwise
                                 parses as a single-key map). Idempotent on exact match.
                                 [--slug FROM --to TARGET --kind work|theory|pending
                                 --gloss "..."]
  cupel related-rm               Remove a related bullet by target slug; tidies up any
                                 empty list-header left behind. [--slug FROM --to TARGET]
  cupel work-set                 Update (or insert) a top-level scalar field in a
                                 dossier's front-matter: backing, source, translator,
                                 layer, verified, etc. Auto-quotes values containing
                                 ': '. New scalars get placed adjacent to existing
                                 scalars (before any list-keys). [--slug SLUG
                                 --field NAME --value VAL]
  cupel vocab-seed               Regenerate the prose allow-list from cupel's
                                 catalogs (engine names + cluster slugs + glossary
                                 slugs + theory/vocab-allowlist.txt), printed to
                                 stdout. The vocabulary recall + gate now live in
                                 calque (the substrate-general drift engine):
                                   cupel vocab-seed > .calque/vocab-allowlist.txt
                                   calque vocab-check        # the gate
                                   calque vocab-report       # the surface
                                   calque synonym-report     # word-level drift
                                 vocab-seed is the cupel-specific part calque can't
                                 know — the catalog→allow-list seed.
                                 [--dir D --allowlist F --clusters F --glossary F]

Wire (global, fires every session) by adding to ~/.claude/settings.json:
  {"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"cupel hook"}]}]}}

Architecture (v2 hybrid): a cheap local embedding GATE runs on every prompt
(ollama, cosine vs cached engine prototypes); only when it trips does the Haiku
LENS adjudicate running-vs-discussing and fix attribution. Fail-safe: no ollama
-> lexical fallback (v1 face_terms); ollama up + no key -> gate-only message;
full stack -> lens decides. Every path swallows errors and never blocks a turn.

Env:
  CUPEL_SKIP=1               short-circuit before any work
  CUPEL_HOOK_COOLDOWN=<sec>  seconds between fires (default 900)
  CUPEL_HOOK_THRESHOLD=<n>   lexical-FALLBACK only: min face-term hits (default 2)
  CUPEL_GATE_THRESHOLD=<f>   embedding-gate cosine cutoff (default 0.57)
  CUPEL_EMBED_MODEL=<name>   ollama embedder (default nomic-embed-text)
  CUPEL_OLLAMA_URL=<url>     ollama base url (default http://localhost:11434)
  CUPEL_LENS_MODEL=<id>      lens model (default claude-haiku-4-5-20251001)
  CUPEL_LENS_DISABLED=1      force gate-only (no API calls) without removing the key
  CUPEL_ENV_FILE=<path>      .env to source the key from (default ~/.claude/cupel/.env)
  CUPEL_HTTP_TIMEOUT_MS=<n>  gate+lens timeout (default 8000)
  CUPEL_ENGINES=<path>       load engines.json from disk (live-tune; default embedded)
  CUPEL_METRICS_DISABLED=1   stop writing per-invocation metrics.jsonl
  CUPEL_CRITIC_MARGIN=<f>    critic: flag engines below this separation margin (default 0.05)
  CUPEL_CRITIC_MAX=<n>       critic: max engines to propose for per run (default 3)
  CUPEL_CRITIC_MODEL=<id>    critic: proposal model (default = lens model)
  CUPEL_CRITIC_CORPUS=<path> critic: JSON corpus override [{"engine","text"}] (default built-in)
  CUPEL_ANCESTOR_DIR=<dir>   build-corpus: dir holding the ancestor pg*.txt (default /tmp; --dir overrides)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(help)
		return
	}
	switch os.Args[1] {
	case "hook":
		runHook()
	case "doctor":
		runDoctor()
	case "mark-fire":
		runMarkFire(os.Args[2:])
	case "critic":
		runCritic(os.Args[2:])
	case "build-corpus":
		runBuildCorpus(os.Args[2:])
	case "build-data":
		runBuildData(os.Args[2:])
	case "coverage":
		runCoverage(os.Args[2:])
	case "quotes-audit":
		runQuotesAudit(os.Args[2:])
	case "tag-audit":
		runTagAudit(os.Args[2:])
	case "merge-cards":
		runMergeCards(os.Args[2:])
	case "works-lint":
		runWorksLint(os.Args[2:])
	case "redaction-audit":
		runRedactionAudit(os.Args[2:])
	case "redaction-hook":
		runRedactionHook()
	case "vocab-seed":
		runVocabSeed(os.Args[2:])
	case "migrate-refs":
		runMigrateRefs(os.Args[2:])
	case "migrate-fm-quotes":
		runMigrateFMQuotes(os.Args[2:])
	case "db-sync":
		runDBSync(os.Args[2:])
	case "db-lint":
		runDBLint(os.Args[2:])
	case "new-work":
		runNewWork(os.Args[2:])
	case "related-add":
		runRelatedAdd(os.Args[2:])
	case "related-rm":
		runRelatedRm(os.Args[2:])
	case "work-set":
		runWorkSet(os.Args[2:])
	case "clusters-lint":
		runClustersLint(os.Args[2:])
	case "glossary-lint":
		runGlossaryLint(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(help)
	default:
		fmt.Fprintf(os.Stderr, "cupel: unknown command %q\n\n%s", os.Args[1], help)
		os.Exit(2)
	}
}
