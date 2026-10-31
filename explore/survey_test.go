//go:build explore

package explore_test

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/monogatari/explore"
)

// The explorer's slow tests (seconds each): go test -tags explore, or
// make explore.

func TestRunFindsEachCheck(t *testing.T) {
	rep := explore.Run(subject(), explore.Budget{Depth: 3, Steps: 1500, TrapSteps: 60})
	if len(rep.Errors) > 0 {
		t.Fatalf("explorer errors: %v", rep.Errors)
	}
	by := rep.ByCheck()
	for _, check := range []string{explore.CheckPanic, explore.CheckErrors, explore.CheckStaticTrap, explore.CheckTrap, "not-last"} {
		if len(by[check]) == 0 {
			t.Errorf("no %s finding:\n%s", check, rep.Text(0))
		}
	}
	for _, f := range by["not-last"] {
		if !slices.Equal(f.Path, []string{"G"}) {
			t.Errorf("not-last finding path %v, want the shortest, G", f.Path)
		}
	}
	for _, f := range by[explore.CheckPanic] {
		if !slices.Equal(f.Path, []string{"b"}) {
			t.Errorf("panic path %v, want b", f.Path)
		}
		if err := explore.Reproduce(subject(), explore.CheckPanic, f.Path); err == nil {
			t.Errorf("Reproduce(%v) found no panic", f.Path)
		}
	}
	if err := explore.Reproduce(subject(), explore.CheckPanic, []string{"j"}); err != nil {
		t.Errorf("Reproduce(j) = %v, want nil", err)
	}
	if got := by[explore.CheckTrap]; len(got) > 0 && !strings.Contains(got[0].Summary, "stuck") {
		t.Errorf("trap finding %q does not name the stuck mode", got[0].Summary)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	b := explore.Budget{Depth: 2, Steps: 300, CompareFull: true, TrapSteps: 30}
	a, c := explore.Run(subject(), b), explore.Run(subject(), b)
	if string(a.JSON()) != string(c.JSON()) {
		t.Fatalf("two runs differ:\n%s\n---\n%s", a.Text(0), c.Text(0))
	}
	if a.States["full"] < a.States["abstract"] {
		t.Errorf("full keying merged more than abstract: %v", a.States)
	}
}
