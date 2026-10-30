package explore

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// Occurrence is one report's finding of a Group.
type Occurrence struct {
	Subject string   `json:"subject"`
	Path    []string `json:"path"`
	Count   int      `json:"count"`
	Weight  int      `json:"weight"` // the subject's size (Dedupe's weight): smaller is simpler
}

// Group is every finding of one check with one normalized summary
// (NormalizeSummary) across reports.
type Group struct {
	Check   string `json:"check"`
	Summary string `json:"summary"`
	Apps    int    `json:"apps"`  // reports showing it
	Count   int    `json:"count"` // times the searches met it, summed
	// Occurrences are one per report, best first: the shortest path, then
	// the simplest subject (smallest weight), then the subject's name.
	Occurrences []Occurrence `json:"occurrences"`
}

// Best is the group's first occurrence.
func (g Group) Best() Occurrence { return g.Occurrences[0] }

// namespaced is a framework operation key in the app namespace
// (app.palette.recall), which NormalizeSummary writes short.
var namespaced = regexp.MustCompile(`\bapp\.([a-z][a-z-]*\.[a-z][a-z-]*)`)

// NormalizeSummary is a finding's summary without what varies between
// applications and runs: quoted ids and numbers (as the explorer already
// strips them from errors), the app. namespace of framework operations
// (so a survey before noun namespaces groups with one after) and runs of
// spaces.
func NormalizeSummary(s string) string {
	s = normalizeError(s)
	s = namespaced.ReplaceAllString(s, "$1")
	return strings.Join(strings.Fields(s), " ")
}

// Dedupe groups the findings of every report by check and normalized
// summary, one Group per pair, sorted by check then summary. weight is a
// subject's size (nil weighs every subject alike). The result depends only
// on the reports' findings, not their order.
func Dedupe(reports []Report, weight func(subject string) int) []Group {
	groups := map[string]*Group{}
	for _, rep := range reports {
		w := 0
		if weight != nil {
			w = weight(rep.Subject)
		}
		for _, f := range rep.Findings {
			sum := NormalizeSummary(f.Summary)
			key := f.Check + "\x00" + sum
			g := groups[key]
			if g == nil {
				g = &Group{Check: f.Check, Summary: sum}
				groups[key] = g
			}
			g.Count += f.Count
			// One occurrence per report: a report holding two findings that
			// normalize alike keeps the better one.
			o := Occurrence{Subject: rep.Subject, Path: slices.Clone(f.Path), Count: f.Count, Weight: w}
			if i := slices.IndexFunc(g.Occurrences, func(p Occurrence) bool { return p.Subject == rep.Subject }); i >= 0 {
				o.Count += g.Occurrences[i].Count
				if compareOccurrence(g.Occurrences[i], o) <= 0 {
					o.Path = g.Occurrences[i].Path
				}
				g.Occurrences[i] = o
				continue
			}
			g.Occurrences = append(g.Occurrences, o)
		}
	}
	out := make([]Group, 0, len(groups))
	for _, k := range sortedKeys(groups) {
		g := groups[k]
		slices.SortFunc(g.Occurrences, compareOccurrence)
		g.Apps = len(g.Occurrences)
		out = append(out, *g)
	}
	slices.SortStableFunc(out, func(a, b Group) int {
		return cmp.Or(strings.Compare(a.Check, b.Check), strings.Compare(a.Summary, b.Summary))
	})
	return out
}

func compareOccurrence(a, b Occurrence) int {
	return cmp.Or(
		cmp.Compare(len(a.Path), len(b.Path)),
		cmp.Compare(a.Weight, b.Weight),
		strings.Compare(a.Subject, b.Subject),
		slices.Compare(a.Path, b.Path),
	)
}
