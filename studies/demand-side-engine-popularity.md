# Demand-side study — does effective engine use track popularity?

**Status:** protocol-validation pilot (pre-registered 2026-05-28, before data).

## Thesis (user, 2026-05-28)
Fiction that uses wish-fulfillment engines effectively is popular; engine-light or badly-structured fiction is not. Stated sharply: **effective engine use correlates with popularity, within genre.**

## Why a pilot
A first blind probe (popular `0/49` resisters vs obscure PD fiction `4/12` + box-office bombs `4/14`) gave a directional signal but was confounded: resisters clustered by *genre/form* (comedy, tragedy, realism, story-collections resist a wish-fulfillment catalog regardless of popularity), engine *presence* proved insufficient (18/26 negative-class works ran a clean engine and still failed), N was tiny, and tagging was single-rater. This pilot validates a design that fixes those before committing to the full ~270-call study.

## Pre-registered predictions (committed before data)
- **P1 (the test):** within each genre, the share of works scored **engine + effectiveness=delivered** is higher in the **top** popularity stratum than the **bottom**. Support threshold: top − bottom ≥ **+15 pp**.
- **P2 (machinery check):** the engine-resistant genre (humor) shows a higher **different-aim** rate than the engine-rich genre (adventure).
- **P3 (measurement check):** inter-rater agreement on the category {engine / different-aim / resister} is ≥ **0.6**. Below that, the rubric needs tightening before the full study.
- **Falsifier:** if `delivered`-rate is flat across strata within genre, this design lends the thesis no support.

## Protocol
- **Sample (pilot):** 2 genres × 3 popularity strata × 6 works = **36 PD works**. Genres: **adventure stories** (expected engine-rich) vs **humorous fiction** (expected engine-resistant). Strata = top / middle / bottom by Gutenberg `download_count` within the genre subject (continuous popularity axis, same source, all groundable).
- **Tagging:** each work scored by **2 independent agent-raters**, each **blind** to the hypothesis, the work's stratum/popularity, and each other. Raters fetch the PD text and judge from the page.
- Each rater returns: **category** ∈ {`engine` (one of the 18) / `different-aim` (not structured to deliver a wish-payout — comedy, satire, realist slice-of-life, spine-less collection) / `resister` (wish-shaped but no confirmed engine fills)}, and **effectiveness** ∈ {`absent` / `nominal` (engine present, slot-2 backing not really paid — hollow payoff) / `delivered` (payoff lands with its backing)}.
- **Return-only — no catalog cards written** (this is a measurement, not seeding).

## Analysis
Per genre: `delivered`-rate by stratum (test the monotonic gradient, P1). Across genres: `different-aim`-rate (P2). Overall + per-cell inter-rater agreement (P3). Caveat retained regardless of result: a within-genre correlation is still correlational — causation (engines→popular vs craft→both) needs a third-factor (craft/budget) control, deferred to the full study.

## Results (pilot, 2026-05-28)

36 PD works, 2 blind raters each. "delivered" below = **both** raters scored engine + effectiveness=delivered (conservative).

| stratum (Gutenberg downloads) | adventure — delivered /6 | humor — delivered /6 | humor — different-aim /6 |
|---|---|---|---|
| top (most downloaded) | 2 (33%) | 3 (50%) | 2 |
| mid | 4 (67%) | 1 (17%) | 2 |
| bottom (least) | 5 (83%) | 0 (0%) | 3 |

- **P1 (delivered higher in top stratum) — SPLIT, and adventure REVERSED it.** Adventure: top 33% → bottom 83% (delivered *rises* as popularity falls, −50pp — opposite the prediction). Humor: top 50% → bottom 0% (+50pp — as predicted). The two genres point opposite ways. P1 fails as stated.
- **P2 (humor more different-aim) — SUPPORTED.** Humor 7/18 different-aim vs adventure 0/18; the genre / different-aim machinery works.
- **P3 (agreement ≥0.6) — PASS, marginal (67%).** Agreement is high on clear pulp (raters concur on Frank Merriwell, Arsène Lupin, sword-and-planet) and low on literary/ambiguous canon (Moby Dick, Huck Finn, Tom Sawyer split engine-vs-different-aim) — reliable exactly where the call is easy.

### The pilot's real finding — the popularity proxy is wrong
The adventure reversal exposes it: **Gutenberg `download_count` measures canonicity / cultural enshrinement, not contemporary mass-market appeal.** Adventure's *top* stratum is the literary canon (Moby Dick, Monte Cristo, Huckleberry Finn, Tom Sawyer) — works that *complicate* the wish and so score lower on clean engine-delivery; its *bottom* stratum is forgotten pulp (sword-and-planet, Frank Merriwell) that delivers its engine cleanly. On the canon axis, clean engine-delivery is if anything *negatively* related to "popularity," because canonization rewards transcending the wish, not delivering it.

**Refined picture — three populations, not two:** (a) **mass-market hits** — clean engine-delivery (the original 0/49 contemporary bestsellers all delivered); (b) **forgotten pulp** — *also* clean engine-delivery, yet unread; (c) **literary canon** — engine-*complicating*, "popular" on Gutenberg for a different reason (assigned, studied). So clean engine use is **necessary-ish for a mass-market hit but predicts neither canon nor obscurity** — execution/freshness/marketing separate the hit from the pulp; literary depth separates the canon from both.

### Verdict for the full study
- **Do NOT use Gutenberg downloads as the popularity axis** — it's a canonicity proxy that inverts the test. Use a contemporary mass-market metric (sales, ratings volume).
- **Separate the three populations explicitly** (mass-market / pulp / canon), or the gradient averages out or reverses.
- **The protocol itself worked** (blind 2-rater, effectiveness rubric, different-aim category, κ≈0.67) — keep it; fix the sampling axis. The pilot did its job: it killed a flawed design before the expensive full run.
