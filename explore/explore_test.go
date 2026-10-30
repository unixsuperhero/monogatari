package explore_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/monogatari/explore"
	"github.com/unixsuperhero/ochazuke"
)

// buggy is an application with a bug per check: b panics, f fails, s
// enters a mode nothing leaves (static and dynamic traps), and selecting
// the last row breaks the application's own check (lastRow).
func buggy() (*ochazuke.Model, error) {
	r := ochazuke.NewRuntime()
	m := ochazuke.NewModel(r)
	m.Appearance.Theme = "mono"
	if err := r.DefineMode("stuck", false); err != nil {
		return nil, err
	}
	r.Define("item").
		Verb("boom", func(ochazuke.Ctx) ochazuke.Result { panic("boom") }).
		Verb("fail", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Failed(errors.New("broken")) }).
		Mode("normal").
		Bind("b", ochazuke.Do("item.boom")).
		Bind("f", ochazuke.Do("item.fail")).
		Bind("s", ochazuke.Do("mode.set", ochazuke.Arguments{"mode": "stuck"}))
	var list *ochazuke.List
	if _, err := r.AddPage("p", "Page", func(r *ochazuke.Runtime, w, h int) ochazuke.Block {
		return ochazuke.Text(list.View(w, h))
	}); err != nil {
		return nil, err
	}
	l, err := r.AddList("l", []ochazuke.ListItem{{ID: "a", Label: "a", Noun: "item"}, {ID: "b", Label: "b", Noun: "item"}, {ID: "c", Label: "c", Noun: "item"}}, ochazuke.Parent("p"))
	if err != nil {
		return nil, err
	}
	list = l
	if err := r.OpenPage("p"); err != nil {
		return nil, err
	}
	return m, r.SetFocus("a")
}

// lastRow is the application's own invariant, broken on purpose: the last
// row must never be selected.
var lastRow = explore.Check{Name: "not-last", Fn: func(m *ochazuke.Model) error {
	if sel, ok := m.Runtime.List("l").Selected(); ok && sel.ID == "c" {
		return errors.New("the last row is selected")
	}
	return nil
}}

func subject() explore.Subject {
	return explore.Subject{Name: "buggy", New: buggy, Checks: []explore.Check{lastRow}}
}

// TestRunFindsTheShallowBugs is the explorer's fast check (the full one,
// TestRunFindsEachCheck, needs -tags explore): one key deep it finds the
// panic, the failing verb and the application's own invariant, each by
// its one-key path.
func TestRunFindsTheShallowBugs(t *testing.T) {
	rep := explore.Run(subject(), explore.Budget{Depth: 1, Steps: 60, TrapSteps: 10})
	if len(rep.Errors) > 0 {
		t.Fatalf("explorer errors: %v", rep.Errors)
	}
	by := rep.ByCheck()
	for check, path := range map[string]string{explore.CheckPanic: "b", explore.CheckErrors: "f", "not-last": "G"} {
		if !slices.ContainsFunc(by[check], func(f *explore.Finding) bool { return slices.Equal(f.Path, []string{path}) }) {
			t.Errorf("no %s finding by %s:\n%s", check, path, rep.Text(0))
		}
	}
}

// TestShrink: a finding found late is shrunk to the keys that matter.
func TestShrink(t *testing.T) {
	seed := []string{"k", "k", "j", "k", "j", "j"}
	s := subject()
	s.Seeds = [][]string{seed}
	rep := explore.Run(s, explore.Budget{Depth: 1, Steps: 50})
	found := false
	for _, f := range rep.ByCheck()["not-last"] {
		found = true
		if !f.Shrunk || f.Found != len(seed) || !slices.Equal(f.Path, []string{"j", "j"}) {
			t.Errorf("not-last finding: path %v found %d shrunk %v, want j j shrunk from %d", f.Path, f.Found, f.Shrunk, len(seed))
		}
	}
	if !found {
		t.Errorf("no not-last finding:\n%s", rep.Text(0))
	}
}

func TestGoTestSnippet(t *testing.T) {
	f := &explore.Finding{Check: "panic", Summary: "update: boom", Path: []string{"j", "b"}}
	got := f.GoTest("subject()")
	for _, want := range []string{"func TestExplorePanicUpdateBoom(t *testing.T)", `explore.Reproduce(subject(), "panic", []string{"j", "b"})`} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet lacks %q:\n%s", want, got)
		}
	}
}
