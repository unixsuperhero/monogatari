package explore_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/unixsuperhero/monogatari/explore"
)

// TestDedupe groups findings by check and normalized summary across
// reports: ids, numbers and the app. namespace do not split a group; per
// group the shortest path wins, then the lighter subject, then the name;
// the order of the reports does not matter.
func TestDedupe(t *testing.T) {
	f := func(check, summary string, count int, path ...string) *explore.Finding {
		return &explore.Finding{Check: check, Summary: summary, Count: count, Path: path}
	}
	a := explore.Report{Subject: "a", Findings: []*explore.Finding{
		f("operation-error", `app.list.mark: row "r1" has 2 marks`, 1, "j", "space"),
		f("honest-hints", "? bindings.help fails when pressed: modal \"bindings-help\" is already open", 3, "?", "?"),
	}}
	b := explore.Report{Subject: "b", Findings: []*explore.Finding{
		f("operation-error", `list.mark: row "x9" has 17 marks`, 2, "space"),
		f("honest-hints", "? bindings.help fails when pressed: modal \"help\" is already open", 1, "?", "?"),
	}}
	c := explore.Report{Subject: "c", Findings: []*explore.Finding{
		f("honest-hints", "? bindings.help fails when pressed: modal \"bindings-help\" is already open", 1, "?", "?"),
	}}
	weight := map[string]int{"a": 5, "b": 9, "c": 1}
	groups := explore.Dedupe([]explore.Report{a, b, c}, func(s string) int { return weight[s] })
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2: %+v", len(groups), groups)
	}
	hints, errs := groups[0], groups[1]
	if hints.Summary != `? bindings.help fails when pressed: modal "…" is already open` || hints.Apps != 3 || hints.Count != 5 {
		t.Errorf("hints group %+v", hints)
	}
	if got := hints.Best(); got.Subject != "c" { // equal paths: the lightest subject
		t.Errorf("hints best %+v, want c", got)
	}
	if errs.Summary != `list.mark: row "…" has # marks` || errs.Apps != 2 {
		t.Errorf("errors group %+v", errs)
	}
	if got := errs.Best(); got.Subject != "b" || !slices.Equal(got.Path, []string{"space"}) { // the shortest path
		t.Errorf("errors best %+v, want b's one key", got)
	}
	if again := explore.Dedupe([]explore.Report{c, b, a}, func(s string) int { return weight[s] }); !reflect.DeepEqual(again, groups) {
		t.Errorf("report order changes the groups:\n%+v\n%+v", again, groups)
	}
}

// TestFrameworkRefusal matches the framework's own refusals by operation
// and message, in either namespace and through macro.record, and nothing
// else.
func TestFrameworkRefusal(t *testing.T) {
	for _, c := range []struct {
		op, err string
		want    bool
	}{
		{"app.history.repeat", "nothing to repeat", true},
		{"palette.run", `"pasted" names no verb or noun here`, true},
		{"app.macro.record", "no palette history", true},
		{"markdown.undo", "nothing to undo", true},
		{"history.repeat", "nothing to undo", false},
		{"bindings.help", `modal "bindings-help" is already open`, false},
		{"list.mark", "list has no selected row", false},
	} {
		if got := explore.FrameworkRefusal(c.op, c.err); got != c.want {
			t.Errorf("FrameworkRefusal(%q, %q) = %v, want %v", c.op, c.err, got, c.want)
		}
	}
}
