package explore

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// SurveyBudget is the budget Survey runs with: small for go test, the
// deep budget (depth 6, 20000 steps) when OCHAZUKE_EXPLORE=deep.
func SurveyBudget() Budget {
	if os.Getenv("OCHAZUKE_EXPLORE") == "deep" {
		return Budget{Depth: 6, Steps: 20000, CompareFull: os.Getenv("OCHAZUKE_EXPLORE_FULL") != ""}
	}
	if raceEnabled {
		return Budget{Depth: 2, Steps: 300} // the race detector makes every replay ten times slower
	}
	return Budget{Depth: 3, Steps: 2000}
}

// Survey explores a subject from a test and logs the report. Findings never
// fail the test (it surveys); the explorer's own failures do: a replay that
// did not reach its state, or two runs on one budget that report
// differently (checked on a smaller budget, except in the deep run). With
// OCHAZUKE_EXPLORE_OUT set the report is written there as NAME.json,
// NAME.txt and NAME.tests.go.txt (a Go test per finding, Report.GoTests).
func Survey(t testing.TB, s Subject) Report {
	t.Helper()
	b := SurveyBudget()
	rep := Run(s, b)
	t.Log("\n" + rep.Text(5) + rep.Legend())
	for _, e := range rep.Errors {
		t.Errorf("explorer: %s", e)
	}
	if os.Getenv("OCHAZUKE_EXPLORE") != "deep" {
		// Determinism: two more runs on a smaller budget report alike.
		small := Budget{Depth: 3, Steps: 300}
		if raceEnabled {
			small = Budget{Depth: 2, Steps: 80}
		}
		if a, b := string(Run(s, small).JSON()), string(Run(s, small).JSON()); a != b {
			t.Errorf("explorer: two runs of %s with the same budget report differently: %s", s.Name, firstDiff(a, b))
		}
	}
	if dir := os.Getenv("OCHAZUKE_EXPLORE_OUT"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = os.WriteFile(dir+"/"+s.Name+".json", rep.JSON(), 0o644)
			_ = os.WriteFile(dir+"/"+s.Name+".txt", []byte(rep.Text(0)+rep.Legend()), 0o644)
			_ = os.WriteFile(dir+"/"+s.Name+".tests.go.txt", []byte(rep.GoTests("subject()")), 0o644)
		}
	}
	return rep
}

// firstDiff names the first line two texts differ on.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < min(len(al), len(bl)); i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d: %q vs %q", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("%d lines vs %d", len(al), len(bl))
}
