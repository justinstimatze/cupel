package main

// clusters.go — parse the cluster table in theory/cluster-catalog.md and emit
// the per-cluster JSON the Astro `/clusters/` pages consume. The markdown file
// is the source of truth; this file derives slugs, splits the prose columns,
// and writes a stable shape. Cluster *numbers* (the row index in the catalog)
// are NOT used as identifiers — slugs are. Renumbering the catalog therefore
// does not break cross-references that name a cluster by slug.

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// clusterSpec is one cluster row from cluster-catalog.md, ready for the Astro
// /clusters/ page to render. RowNumber is preserved only as a display detail
// (the column heading reads "Row N in the catalog"); cross-refs and routes use
// Slug.
type clusterSpec struct {
	RowNumber      int    `json:"row_number"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Candidate      bool   `json:"candidate,omitempty"`
	Domain         string `json:"domain"`
	IntroMD        string `json:"intro_md,omitempty"`
	StatusProse    string `json:"status_prose"`
	EnginesProse   string `json:"engines_prose"`
	SpecimensProse string `json:"specimens_prose"`
}

// clusterRow matches the catalog's table row shape:
//
//	| N | **Name**[ (candidate)] | Domain | Status | Engines | Specimens |
var clusterRow = regexp.MustCompile(`^\|\s*(\d+)\s*\|\s*\*\*([^*]+)\*\*(\s*\(candidate\))?\s*\|\s*(.+?)\s*\|\s*(.+?)\s*\|\s*(.+?)\s*\|\s*(.+?)\s*\|\s*$`)

// loadClusters reads cluster-catalog.md and returns the per-cluster specs.
// It rejects rows where the row number is missing or unparseable; everything
// downstream is filled from the matched groups. After parsing rows, it
// walks the same file for the "## Cluster intros" section and attaches
// each `### <Name>` subsection's body prose to the matching cluster.
func loadClusters(catalogPath string) ([]clusterSpec, error) {
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("clusters: read %s: %w", catalogPath, err)
	}
	var out []clusterSpec
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		mm := clusterRow.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		num := 0
		fmt.Sscanf(mm[1], "%d", &num)
		name := strings.TrimSpace(mm[2])
		out = append(out, clusterSpec{
			RowNumber:      num,
			Name:           name,
			Slug:           clusterSlug(name),
			Candidate:      strings.TrimSpace(mm[3]) != "",
			Domain:         strings.TrimSpace(mm[4]),
			StatusProse:    strings.TrimSpace(mm[5]),
			EnginesProse:   strings.TrimSpace(mm[6]),
			SpecimensProse: strings.TrimSpace(mm[7]),
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("clusters: scan %s: %w", catalogPath, err)
	}
	attachClusterIntros(out, string(raw))
	return out, nil
}

// attachClusterIntros walks the "## Cluster intros" section of the catalog
// markdown. For each `### <Name>` subsection, it joins the following
// paragraph lines and attaches them to the matching cluster by name. The
// match is case-insensitive on the cluster's display name (handles
// "Polyamory canon" -> the "Polyamory canon" entry whose row says
// "**Polyamory canon** (candidate)").
func attachClusterIntros(clusters []clusterSpec, body string) {
	const introHeader = "## Cluster intros"
	idx := strings.Index(body, introHeader)
	if idx < 0 {
		return
	}
	section := body[idx:]
	// End the section at the next `## ` heading (a top-level section).
	if end := strings.Index(section[len(introHeader):], "\n## "); end >= 0 {
		section = section[:len(introHeader)+end]
	}
	// Index clusters by normalized name for matching.
	byName := map[string]int{}
	for i, c := range clusters {
		byName[strings.ToLower(strings.TrimSpace(c.Name))] = i
	}
	// Walk the section, picking up `### <Name>` and the following paragraph.
	var (
		currentName string
		buf         []string
	)
	flush := func() {
		if currentName == "" || len(buf) == 0 {
			return
		}
		if i, ok := byName[strings.ToLower(strings.TrimSpace(currentName))]; ok {
			clusters[i].IntroMD = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		currentName = ""
		buf = buf[:0]
	}
	for _, ln := range strings.Split(section, "\n") {
		if strings.HasPrefix(ln, "### ") {
			flush()
			currentName = strings.TrimSpace(strings.TrimPrefix(ln, "### "))
			continue
		}
		if currentName == "" {
			continue
		}
		buf = append(buf, ln)
	}
	flush()
}

// clusterSlug derives the stable URL slug from the cluster name. The cluster's
// markdown name (e.g. "Wisdom-tradition-compression") becomes the lowercase
// slug (e.g. "wisdom-tradition-compression"); slashes go away, spaces go to
// hyphens, hyphens are preserved.
func clusterSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}
