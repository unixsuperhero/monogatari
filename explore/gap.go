package explore

import (
	"slices"
	"strings"
	"testing"
)

// frameworkRefusals are the framework's own operation errors that refuse
// work there is nothing to do for (an empty history, register or match,
// a word the palette cannot complete): the explorer presses into them on
// every application and none is a bug. The runtime has no sentinel errors
// for them (a Result carries text), so each is matched by its operation
// and message; a message ending in "…" matches by its tail.
var frameworkRefusals = map[string][]string{
	"history.repeat":       {"nothing to repeat"},
	"palette.recall":       {"no palette history"},
	"palette.run":          {"missing verb or noun", "… names no verb or noun here"},
	"palette.complete":     {"no completion highlighted (ctrl+n / ctrl+p)"},
	"palette.move":         {"no completions for this word"},
	"chooser.pick":         {"nothing matches"},
	"macro.replay":         {"macro register is empty", "register must be one character"},
	"macro.start":          {"register must be one character"},
	"register.select":      {"register must be one character"},
	"input.begin":          {"single-line input cannot open a new line"},
	"input.paste":          {"single-line input cannot paste multiline yank"},
	"markdown.undo":        {"nothing to undo"},
	"markdown.redo":        {"nothing to redo"},
	"setting-value.commit": {"an item cannot be empty"},
	"page.next":            {"no pages registered"},
	"page.previous":        {"no pages registered"},
}

// FrameworkRefusal is a Subject.Expected for the framework's own
// refusals (frameworkRefusals): nothing to undo, redo or repeat, no
// palette history, a palette line or completion that names nothing, a
// chooser query nothing matches, an empty or misnamed register, a
// single-line input asked for a new line or a multiline paste, an empty
// list item, page motions with no pages. The operation may be written in
// the app namespace (app.palette.run) or be macro.record, which
// re-dispatches the key and fails with its request's error. Compose it
// with an application's own: Expected: func(op, err string) bool { return
// explore.FrameworkRefusal(op, err) || mine(op, err) }.
func FrameworkRefusal(op, err string) bool {
	op = strings.TrimPrefix(op, "app.")
	match := func(msgs []string) bool {
		return slices.ContainsFunc(msgs, func(m string) bool {
			if tail, ok := strings.CutPrefix(m, "… "); ok {
				return strings.HasSuffix(err, " "+tail)
			}
			return err == m
		})
	}
	if op == "macro.record" {
		for _, k := range sortedKeys(frameworkRefusals) {
			if match(frameworkRefusals[k]) {
				return true
			}
		}
		return false
	}
	return match(frameworkRefusals[op])
}

// Shows replays path on the subject and reports whether check still shows
// there with the error summary names (a Finding's or Group's summary: the
// part after "fails when pressed: " for honest-hints, after the operation
// for operation-error). The error is the explorer's own (a replay that
// failed), never the finding.
func Shows(s Subject, check, summary string, path []string) (bool, error) {
	err := Reproduce(s, check, path)
	if err == nil {
		return false, nil
	}
	head, rest, _ := strings.Cut(err.Error(), "\n")
	if !strings.HasPrefix(head, check+" after ") {
		return false, err
	}
	want := summaryError(summary)
	return slices.ContainsFunc(strings.Split(rest, "\n"), func(l string) bool {
		return strings.Contains(NormalizeSummary(l), want)
	}), nil
}

// summaryError is the error a finding's summary names.
func summaryError(summary string) string {
	if _, after, ok := strings.Cut(summary, "fails when pressed: "); ok {
		return after
	}
	if _, after, ok := strings.Cut(summary, ": "); ok {
		return after
	}
	return summary
}

// Gap is a known gap as a test, the Go form of a skipped gap story: it
// replays path and always skips with "gap: CHECK: SUMMARY (passes now:
// BOOL)", true once the path no longer shows the finding (then fix the
// story or delete the gap). A replay that fails is a test failure.
func Gap(t testing.TB, s Subject, check, summary string, path []string) {
	t.Helper()
	shows, err := Shows(s, check, summary, path)
	if err != nil {
		t.Fatalf("gap %s: %s: %v", check, summary, err)
	}
	t.Skipf("gap: %s: %s (passes now: %v)", check, summary, !shows)
}
