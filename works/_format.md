# The works format

A **work** is cupel's product unit: one file per work, with the analytical reading and the verbatim evidence both inside it. The browsable surface renders one card per work; each card expands inline to its reading and links into the same file's evidence section. Before the 2026-06-06 merger this was two files — `reviews/<slug>.md` (the distilled product card) plus `entries/<slug>.md` (the verbatim-cited dossier). One file per work captures both layers without the cross-reference tax.

## File shape

```
---
work: <title>
author: <author>
year: <year>
medium: novel | story | film | game | comic | scripture | nonfiction | manifesto |...
backing: slot-proven | reviewed | wikipedia-grounded | mixed | pilot
source: <bibliographic citation; required when backing = slot-proven for in-copyright works>
engines: [<engine>, <engine>,...]
layer: content | consumption | mixed
verified: true | false
status: <one-line status if relevant>
author_note: <one-line ⚠ badge for well-documented author-conduct concerns; turns the card into a case-study not a platform>
---

## The reading

**The bead.** <one sentence: the single wish this work most services — the precious
metal left after the surface is burned off. This is the cupel claim about the work.>

**Engines**
- **<engine>** · <layer> · <spine|also-runs|...> · <✓|~> — <one line: how it runs here>
- **<engine>** ·...

**The bundle.** <one or two lines: how the engines compose — the gestalt the reader
actually buys. Omit if the work runs a single engine.>

**Dual-use read.** <the counterfeit(s) the engine(s) carry: the "same machine also
sells …" line, then the value-flow call (does the work's gratification *enable* the real
slot-2 work, or model *substituting* the badge for it?). Mandatory — it is the thesis.>

**Consumption.** <if the work is also held as a tribal badge, the consumption-layer
read; else omit.>

**Verdict.** <one crisp line.>

**Evidence.** <`✓ slot-proven` or `~ reviewed` plus bibliographic / source anchors and
cross-refs.>

## The evidence ← present only when `backing: slot-proven`

<exhaustive, every slot filled with a verbatim primary-source quote, the one-rule
in force. The reading distills; the evidence proves.>

### <Engine 1> — slot anchors

<slot 1 / slot 2 / slot 3 quotes, citations, and discussion>

### <Engine 2> — slot anchors
...
```

## Why one file per work

Before the merger the catalog ran two artifacts: **reviews** distilled, **entries** proved. The cross-reference tax — every analytical claim had to point at its evidence file by slug, every dossier had to anchor back to its review — scaled with the corpus. One file per work captures the analytical layer at the top under `## The reading` and the evidentiary layer below under `## The evidence`. The reader gets both without leaving the page; the author edits one file when both layers move.

## Field rules

- **The bead** leads, always. It is the single-sentence wish — cupel's whole move is burning a work down to the bead, so the card states it first. One wish, even for a bundle (name the dominant one; the bundle line handles the rest).
- **Engines** is a tagged list, not prose. Each row carries four facets and a one-liner:
 - *engine* — a confirmed engine, or a candidate (mark "(candidate)") or an excluded probe (mark "(tested, excluded)") where relevant.
 - *layer* — `content` (the wish inside the story), `consumption` (the work as a badge), or a finer-grained taxonomy that emerged in slot-tests (`reader-engine layer`, `character-engine layer`).
 - *role* — `spine` (the engine the work is built to deliver) or `also-runs` (a secondary engine whose slots also fill). Other roles like "cure-shaped institution refused" / "not a spine engine here" surface from specific slot-tests.
 - *evidence tier* — `✓` slot-proven or `~` reviewed.
 - *one-liner* — how the engine runs *in this work*, concretely.
- **The bundle** is where composition gets named — the romance bundle (being-desired + abundance + a protective partner), the superhero bundle (the double life + apotheosis + belonging), etc. Single-engine works omit it.
- **Dual-use read is mandatory.** A cupel reading without the counterfeit is just a reading. Name the counterfeit, then make the value-flow call (the subjective gate, per the README) — and say it is subjective.
- **Never state engine counts** anywhere (the project-wide no-count convention). Describe by name and status.
- **Show contested readings, don't resolve them** — if two engines plausibly fit the spine, list both and say why it is contested (the Bechdel discipline carried up to the product layer).
- **Concise.** The reading's fields are one to three lines each. If a claim needs a paragraph of defense, that defense belongs in `## The evidence`, not `## The reading`.

## Evidence tiers (the integrity rule)

The primary-source one-rule stays in force for slot **validation** — an engine earns confirmation only on verbatim quotes. But a reading may discuss a work cupel has *not* slot-validated, using secondary sources (reviews, recaps, criticism). To keep the two from blurring, every engine tag is marked:

- **✓ slot-proven** — backed by a `## The evidence` section whose slots are filled with verified verbatim quotes. The reading is a faithful compression of proven claims.
- **~ reviewed** — asserted from a reading or secondary sources, *not* slot-validated. A defensible read, flagged as not yet proven on the page.

`backing:` in the front-matter is `slot-proven` (all ✓), `reviewed` (all ~), `wikipedia-grounded` (all ~ against named secondary sources), `mixed` (some ✓ some ~), or `pilot` (per-chapter / methodology work). A reader can therefore tell at a glance how much of a card rests on verified text versus analysis. **Secondary sources are allowed for ~ claims; they are never allowed to fill a slot.** A ~ engine is promoted to ✓ only by writing the evidence section.

### Sourcing discipline for ~ cards

Training-data familiarity with a famous work is *not* a sufficient basis for a ~ card. Even at the reviewed tier, a card must rest on a checkable source. Acceptable bases, in descending order of strength:

1. **A reading.** The author of the card has actually read/watched the work and is writing from that reading.
2. **A local primary text.** The work is available locally and the card's specific claims can be checked against it.
3. **A consulted secondary source.** A reliable plot/character summary (Wikipedia, the publisher's official synopsis, a reputable critical encyclopedia) was consulted *during* writing and is cited in the `Evidence` line.

Cards written from pure training-data recall — confident-sounding gloss about a work the author hasn't recently checked — are explicitly *not* allowed.

In practice this means:
- Every ~ card's `Evidence` line names what the read is grounded in: *"verified against [URL] (specific claims X, Y)"* for secondary-source cards, *"from the [year edition / film]"* for direct-reading cards.
- Specific named details — character names, exact quoted phrases, plot beats with confident texture — are only included if they appear in the cited source or can be checked against it. When in doubt, soften to the abstraction level the source supports.
- The engine *read* (the interpretive claim about which engines a work runs) is what the reviewer is judging — it's a defensible reading, not a fact to verify. But the specific *evidence* a reviewer adduces to support that reading must be checkable.

### Verbatim quotes and named-scene specifics: the gist-stable / specifics-unstable rule

Subtitle / primary-text audits of in-copyright TV and film cards on 2026-05-30 (Fleabag, Pulp Fiction, Inglourious Basterds, Mad Men, Succession, Atlanta) revealed a *systematic* recall-failure pattern when reviewers reach for cultural-canon dialogue or named scenes from training-data memory: **the gist is stable, the specifics are not.** The conceptual reference — that the show has a fourth-wall-recognition moment, that the Tarantino film has an Ezekiel monologue, that the Succession finale ends with a corporate-power transfer — is reliably recalled. But the specifics layered on top of that gist are unreliable. The pattern: **the more confident the recall feels, the more important it is to check the specifics.**

Operational rules:

1. **Any embedded verbatim quote** must be verified against a primary source (local copy, PD remote fetch, subtitle audit, screenplay database, etc.) before it lands in a card. If verification is not available, paraphrase at the abstraction level the secondary sources support.
2. **Any named episode, scene, venue, or proper noun** beyond what's documented in a consulted secondary source counts as a specific that needs anchoring.
3. **When grepping for a recalled line**, include variant spellings (contracted/uncontracted, comma/no-comma, ellipsis/no-ellipsis). The gist-recall reconstructs the *most-likely-form* not the *actual-form*.
4. **The card itself should record the audit anchor** when it's been done — `Subtitle audited via file_id NNNN; quote verified verbatim` in the Evidence line.

The recovery doc at keeps the running audit log; the rule above is the format-spec form of what that log surfaces.

## Render contract — what the renderer expects

`cupel render` parses these files into a self-contained, filterable HTML browser (`site/index.html`, gitignored/regenerable). Facets: engine tag + evidence tier + work/author search. The card format is a *parsed contract*:

- keep the frontmatter `work` field — the renderer drops files without it
- keep the `- **engine** · layer · role · ✓|~` bullet shape — the renderer drops bullets that don't match
- keep the `## The reading` heading — the renderer's index-card expansion shows only the reading
- keep the `## The evidence` heading when `backing: slot-proven` — the index-card "the evidence →" link anchors here
- keep the `**The bead.**` / `**Verdict.**` lead-ins — the renderer extracts the bead by its lead-in
- `cupel works-lint` enforces all of the above; the pre-commit hook fails on drift

## Worked examples

- [Pride and Prejudice](/cupel/works/pride-and-prejudice/) — a clean single-engine card (repricing, slot-proven).
- [The Scarlet Pimpernel](/cupel/works/the-scarlet-pimpernel/) — a bundle card (the double life + also-runs), showing composition and a multi-face dual-use read.
- [A Little Life](/cupel/works/a-little-life/) — a wound specimen card (slot-proven, in-copyright) with the dual-layer counterfeit (content + consumption) named explicitly.
- [The Holy Bible (KJV 1611)](/cupel/works/the-bible/) — the catalog's highest-engine-density specimen (16 confirmed + 2 queued at the recruitment-register boundary).

These dogfood every field. The render target reads these files; `works-lint` fails the build if any of them break the contract.
