package main

// coverage.go — a rollup over the parsed review cards, two views:
//   (a) works-per-engine — the under-representation gaps (which confirmed engines
//       are thinly reviewed: either we haven't covered them yet, or they are
//       genuinely rarer in popular fiction — a real demand-distribution signal).
//   (b) engines-per-work — the max-bundle leaderboard (which works run the most
//       engines at once; a check on the "engines compose" claim).
// Reuses render.go's loadWorks + loadEngines so the counts can never drift
// from what the browser renders. Pure text: no ollama, no key, no dependencies.

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func runCoverage(args []string) {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	works := fs.String("works", "works", "directory of works/*.md")
	readme := fs.String("readme", "README.md", "README.md (source of the confirmed-engine list)")
	top := fs.Int("top", 10, "how many works to show in the max-bundle leaderboard")
	if err := fs.Parse(args); err != nil {
		return
	}

	cards := loadWorks(*works)
	if len(cards) == 0 {
		fmt.Fprintln(os.Stderr, "cupel coverage: no works in", *works)
		os.Exit(1)
	}
	specs := loadEngines(*readme, cards)

	resisters := 0
	for _, c := range cards {
		tagged := false
		for _, e := range c.Engines {
			if !e.Excluded {
				tagged = true
				break
			}
		}
		if !tagged {
			resisters++
		}
	}

	fmt.Printf("cupel coverage — %d cards · %d confirmed engines · %d resister(s)\n\n",
		len(cards), len(specs), resisters)

	// (a) works per engine — thinnest first (the gaps). loadEngines already
	// dedupes per card and drops excluded tags, so len(Works) is the card count.
	sort.SliceStable(specs, func(i, j int) bool { return len(specs[i].Works) < len(specs[j].Works) })
	fmt.Println("works per engine (thinnest first — the coverage gaps):")
	for _, s := range specs {
		fmt.Printf("  %3d  %-24s %s\n", len(s.Works), s.Name, strings.Repeat("█", len(s.Works)))
	}

	// (b) engines per work — the max-bundle leaderboard.
	type bundle struct {
		Work string
		Eng  []string
	}
	var bundles []bundle
	for _, c := range cards {
		seen := map[string]bool{}
		var names []string
		for _, e := range c.Engines {
			k := strings.ToLower(e.Name)
			if e.Excluded || seen[k] {
				continue
			}
			seen[k] = true
			names = append(names, e.Name)
		}
		bundles = append(bundles, bundle{Work: c.Work, Eng: names})
	}
	sort.SliceStable(bundles, func(i, j int) bool { return len(bundles[i].Eng) > len(bundles[j].Eng) })
	fmt.Printf("\nmax-bundle leaderboard (most engines at once, top %d):\n", *top)
	for i, b := range bundles {
		if i >= *top {
			break
		}
		fmt.Printf("  %d  %-34s %s\n", len(b.Eng), b.Work, strings.Join(b.Eng, ", "))
	}
}
