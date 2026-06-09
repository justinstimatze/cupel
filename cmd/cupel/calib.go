package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// fireTag is one verdict on a past fire, joined to fires.jsonl by hook_event_id.
type fireTag struct {
	Ts          string `json:"ts"`
	HookEventID string `json:"hook_event_id"`
	Verdict     string `json:"verdict"`
	Notes       string `json:"notes,omitempty"`
}

var validVerdicts = map[string]bool{"useful": true, "mixed": true, "not-useful": true}

// runMarkFire tags a fire (by hook_event_id, from fires.jsonl / hook.log) with a
// verdict — the calibration signal that turns the fire log from theatre into a
// hit-rate the engine signatures can be tuned against.
func runMarkFire(args []string) {
	notes := ""
	var pos []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--notes" && i+1 < len(args) {
			notes = args[i+1]
			i++
			continue
		}
		pos = append(pos, args[i])
	}
	if len(pos) < 2 || !validVerdicts[pos[1]] {
		fmt.Fprintln(os.Stderr, "usage: cupel mark-fire <hook_event_id> <verdict> [--notes \"...\"]\n  verdict: useful | mixed | not-useful")
		os.Exit(2)
	}
	appendJSONL("fire-tags.jsonl", fireTag{
		Ts:          nowTs(),
		HookEventID: pos[0],
		Verdict:     pos[1],
		Notes:       notes,
	})
	fmt.Printf("tagged fire %s as %s\n", pos[0], pos[1])
}

// runDoctor rolls up the calibration logs: fire-rate (the sparsity check),
// latency, why silent runs were gated, recent fires, and the verdict tally.
func runDoctor() {
	dir := cupelDir()

	// metrics.jsonl → invocations, fires, latency, gated reasons, v2 telemetry.
	var total, fired, lensCount int
	var sumMs, sumLensMs int64
	gated := map[string]int{}
	gateMode := map[string]int{}
	scanJSONL(filepath.Join(dir, "metrics.jsonl"), func(line []byte) {
		var m metricsRecord
		if json.Unmarshal(line, &m) != nil {
			return
		}
		total++
		sumMs += m.TotalMs
		if m.GateMode != "" {
			gateMode[m.GateMode]++
		}
		if m.LensMs > 0 {
			sumLensMs += m.LensMs
			lensCount++
		}
		if m.Fired {
			fired++
		} else if m.Gated != "" {
			gated[m.Gated]++
		}
	})

	// fire-tags.jsonl → latest verdict per event.
	verdictOf := map[string]string{}
	scanJSONL(filepath.Join(dir, "fire-tags.jsonl"), func(line []byte) {
		var t fireTag
		if json.Unmarshal(line, &t) == nil && t.HookEventID != "" {
			verdictOf[t.HookEventID] = t.Verdict
		}
	})

	// fires.jsonl → engine tally + recent + verdict join + lens-health.
	engineCount := map[string]int{}
	verdictTally := map[string]int{"useful": 0, "mixed": 0, "not-useful": 0, "untagged": 0}
	lensVerdictCount := map[string]int{}
	var recent []fire
	scanJSONL(filepath.Join(dir, "fires.jsonl"), func(line []byte) {
		var f fire
		if json.Unmarshal(line, &f) != nil {
			return
		}
		engineCount[f.Engine]++
		if f.LensVerdict != "" {
			lensVerdictCount[f.LensVerdict]++
		}
		if v, ok := verdictOf[f.HookEventID]; ok {
			verdictTally[v]++
		} else {
			verdictTally["untagged"]++
		}
		recent = append(recent, f)
	})

	fmt.Println("cupel doctor — dual-use hook calibration")
	fmt.Println(strings.Repeat("─", 44))
	if total == 0 {
		fmt.Println("no invocations logged yet (~/.claude/cupel/metrics.jsonl empty).")
		fmt.Println("install the hook (see cmd/cupel/README.md) and let it run.")
		return
	}
	rate := 100 * float64(fired) / float64(total)
	fmt.Printf("invocations: %d   fires: %d   fire-rate: %.1f%%   avg latency: %dms\n",
		total, fired, rate, sumMs/int64(total))
	if rate > 10 {
		fmt.Printf("  ⚠ fire-rate above ~10%% — likely too noisy; raise CUPEL_GATE_THRESHOLD (or tighten face_terms).\n")
	}
	if len(gateMode) > 0 {
		fmt.Printf("gate mode: %s\n", sortedCounts(gateMode))
	}
	if lensCount > 0 {
		fmt.Printf("lens calls: %d   avg lens latency: %dms\n", lensCount, sumLensMs/int64(lensCount))
	}
	// lens-health: every fire records the lens verdict (fired | skipped | error
	// | n/a). A high error rate means the lens model is returning unparseable
	// output, which under the recall-first fail-safe converts to fire-on-error
	// (false positives). Surface the breakdown so the failure mode is visible
	// in routine doctor runs, not buried in the hook log.
	if fired > 0 && len(lensVerdictCount) > 0 {
		fmt.Printf("lens verdicts on fires: %s\n", sortedCounts(lensVerdictCount))
		if errs := lensVerdictCount["error"]; errs > 0 {
			rate := 100 * float64(errs) / float64(fired)
			switch {
			case rate >= 50:
				fmt.Printf("  ⚠ lens-error rate %.0f%% of fires — every gate-trip is firing without a parseable lens verdict. Check CUPEL_LENS_MODEL output and `parseLensResponse` in cmd/cupel/lens.go.\n", rate)
			case rate >= 10:
				fmt.Printf("  ⚠ lens-error rate %.0f%% of fires — investigate; recent fires may include false positives the lens couldn't reject.\n", rate)
			}
		}
	}
	if len(gated) > 0 {
		fmt.Printf("silent breakdown: %s\n", sortedCounts(gated))
	}
	if len(engineCount) > 0 {
		fmt.Printf("fires by engine: %s\n", sortedCounts(engineCount))
	}
	fmt.Printf("verdicts: useful=%d mixed=%d not-useful=%d untagged=%d\n",
		verdictTally["useful"], verdictTally["mixed"], verdictTally["not-useful"], verdictTally["untagged"])
	if n := countCriticProposals(); n > 0 {
		fmt.Printf("open critic proposals: %d   (cupel critic — review ~/.claude/cupel/critic-proposals/)\n", n)
	}
	if n := len(recent); n > 0 {
		fmt.Println("recent fires:")
		for i := max(0, n-5); i < n; i++ {
			f := recent[i]
			gauge := f.LensVerdict
			if f.GateMode == "embedding" {
				gauge = fmt.Sprintf("sim=%.2f/%s", f.GateSim, f.LensVerdict)
			}
			fmt.Printf("  %s  %-16s %-18s %-8s [%s]  %q\n",
				shortDate(f.TS), f.Engine, gauge, verdictOf[f.HookEventID], f.HookEventID, f.PromptSnippet)
		}
		fmt.Println("tag a fire:  cupel mark-fire <id> <useful|mixed|not-useful>")
	}
}

func scanJSONL(path string, fn func([]byte)) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if len(strings.TrimSpace(string(b))) > 0 {
			fn(b)
		}
	}
}

func sortedCounts(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	var parts []string
	for _, e := range s {
		parts = append(parts, fmt.Sprintf("%s=%d", e.k, e.v))
	}
	return strings.Join(parts, " ")
}

func shortDate(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("01-02 15:04")
	}
	return ts
}
