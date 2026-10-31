package explore_test

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/monogatari/explore"
)

// TestObserveSeesEveryStepInOrder: Budget.Observe is told about the
// initial state, every action the search plays, in order, and the end of
// the trap search; every finding of the report turns up in some step; and
// the report is the same bytes with and without an observer, even one
// that draws every frame.
func TestObserveSeesEveryStepInOrder(t *testing.T) {
	s := subject()
	s.Seeds = [][]string{{"j"}}
	b := explore.Budget{Depth: 2, Steps: 120, TrapSteps: 10, CompareFull: true}
	headless := explore.Run(s, b)

	var steps []explore.Step
	frames := 0
	b.Observe = func(st explore.Step) {
		if f := st.Frame(); f != "" {
			frames++
		}
		st.Frame = nil
		steps = append(steps, st)
	}
	watched := explore.Run(s, b)
	if string(headless.JSON()) != string(watched.JSON()) {
		t.Fatalf("an observer changed the report:\n%s\n---\n%s", headless.Text(0), watched.Text(0))
	}
	if headless.Text(0) == "" || stripElapsed(headless.Text(0)) != stripElapsed(watched.Text(0)) {
		t.Fatalf("an observer changed the text report")
	}
	if len(steps) == 0 || steps[0].Phase != explore.PhaseStart || len(steps[0].Path) != 0 || !steps[0].New {
		t.Fatalf("first step %+v, want the initial state", steps[0])
	}
	if steps[1].Phase != explore.PhaseStart || !slices.Equal(steps[1].Path, []string{"j"}) {
		t.Fatalf("second step %+v, want the seed", steps[1])
	}
	if last := steps[len(steps)-1]; last.Phase != explore.PhaseTraps {
		t.Fatalf("last step %+v, want the trap search's end", last)
	}
	search, prev := 0, 0
	seen := map[string]bool{}
	for i, st := range steps {
		if st.Seq != i+1 {
			t.Fatalf("step %d has Seq %d", i, st.Seq)
		}
		if st.Steps < prev || st.Budget != 120 || st.Depth != 2 {
			t.Fatalf("step %d: steps %d after %d, budget %d depth %d", i, st.Steps, prev, st.Budget, st.Depth)
		}
		prev = st.Steps
		if st.Phase == explore.PhaseSearch {
			search++
			if st.Steps != search || st.Action != st.Path[len(st.Path)-1] {
				t.Fatalf("search step %d: Steps %d Action %q Path %v", search, st.Steps, st.Action, st.Path)
			}
		}
		for _, f := range st.Findings {
			seen[f.Check+": "+f.Summary] = true
		}
	}
	if search != watched.Steps {
		t.Fatalf("observed %d search steps, the report played %d", search, watched.Steps)
	}
	if frames == 0 {
		t.Fatal("no step drew a frame")
	}
	for _, f := range watched.Findings {
		if !seen[f.Check+": "+f.Summary] {
			t.Errorf("finding %s: %s never turned up in a step", f.Check, f.Summary)
		}
	}
	if len(seen) != len(watched.Findings) {
		t.Errorf("steps carried %d findings, the report has %d", len(seen), len(watched.Findings))
	}

	// The same budget gives the same steps.
	var again []explore.Step
	b.Observe = func(st explore.Step) { st.Frame = nil; again = append(again, st) }
	explore.Run(s, b)
	if len(again) != len(steps) {
		t.Fatalf("a second run observed %d steps, the first %d", len(again), len(steps))
	}
	for i := range steps {
		if steps[i].State != again[i].State || !slices.Equal(steps[i].Path, again[i].Path) || len(steps[i].Findings) != len(again[i].Findings) {
			t.Fatalf("step %d differs: %+v vs %+v", i, steps[i], again[i])
		}
	}
}

// TestFramesReplaysAPath: one frame before the path and one after each
// key; j moves the selection.
func TestFramesReplaysAPath(t *testing.T) {
	frames, err := explore.Frames(subject(), []string{"j", "j"})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[0] == "" || frames[0] == frames[1] {
		t.Fatalf("frames: %d, first two equal %v", len(frames), len(frames) > 1 && frames[0] == frames[1])
	}
}

func stripElapsed(text string) string {
	first, rest, _ := strings.Cut(text, "\n")
	if i := strings.LastIndex(first, ", "); i >= 0 && strings.HasSuffix(first, "s") {
		first = first[:i]
	}
	return first + "\n" + rest
}
