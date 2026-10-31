package explore

import (
	"fmt"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// The framework's checks, by the names findings are grouped under.
const (
	CheckPanic      = "panic"           // Update, View, a command or a check panicked
	CheckFocusLevel = "focus-top-level" // the focus is inside the top modal (else the page)
	CheckPageFocus  = "page-holds-focus"
	CheckMode       = "mode-registered"
	CheckRowSelect  = "row-selected" // a focused list row is its list's selected row
	CheckRender     = "render-fits"
	CheckHints      = "honest-hints"
	CheckTrap       = "no-trap"
	CheckJobs       = "jobs-settle"
	CheckErrors     = "operation-error"
	CheckSettle     = "commands-settle" // one action delivered more than maxMessages messages
	CheckStaticTrap = "static-trap"
)

// problem is one failed check in a state or after an action: Sig is what
// shrinking must reproduce (the check and its reason without volatile
// detail), Detail the full text.
type problem struct {
	Check  string
	Sig    string
	Detail string
}

// stateProblems runs the state checks on a session standing in a state:
// the focus invariants and the subject's checks. Render checks are
// separate (renderProblems), as rendering at other sizes changes the
// session.
func stateProblems(ss *session) []problem {
	if ss.panic != nil || ss.quit {
		return nil
	}
	r := ss.m.Runtime
	var out []problem
	add := func(check, sig, detail string) { out = append(out, problem{check, check + ": " + sig, detail}) }
	if !r.FocusInTopLevel() {
		add(CheckFocusLevel, "focus outside the top level", fmt.Sprintf("focus %q, top modal %q, page %q", r.Focus, r.TopModal(), r.Page))
	}
	if r.Page != "" && !slices.Contains(r.Ancestry(r.BaseFocus()), r.Page) {
		add(CheckPageFocus, "page is not an ancestor of the base focus", fmt.Sprintf("page %q, base focus %q", r.Page, r.BaseFocus()))
	}
	if !r.ModeKnown() {
		add(CheckMode, "mode "+r.Mode.Name+" is not registered", fmt.Sprintf("mode %q payload %+v", r.Mode.Name, r.Mode.Payload))
	}
	if row, list := rowOf(r, r.Focus); row {
		if sel, ok := r.List(list).Selected(); !ok || sel.ID != r.Focus {
			add(CheckRowSelect, "focused row is not its list's selected row", fmt.Sprintf("focus %q, list %q selects %q", r.Focus, list, sel.ID))
		}
	}
	for _, c := range ss.s.Checks {
		if err := runCheck(c, ss.m); err != nil {
			add(c.Name, firstLine(err.Error()), err.Error())
		}
	}
	return out
}

func runCheck(c Check, m *ochazuke.Model) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("check panicked: %v\n%s", v, trimStack(debug.Stack()))
		}
	}()
	return c.Fn(m)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// renderProblems draws the frame at every size and reports lines wider
// than the width, more lines than the height, and a cursor outside.
func renderProblems(ss *session, sizes [][2]int) []problem {
	var out []problem
	for _, size := range sizes {
		if ss.panic != nil || ss.quit {
			return out
		}
		w, h := size[0], size[1]
		view, err := ss.render(w, h)
		if err != nil {
			out = append(out, problem{CheckPanic, CheckPanic + ": view at " + sizeText(w, h), ss.panic.Value + "\n" + ss.panic.Stack})
			return out
		}
		at := sizeText(w, h)
		lines := strings.Split(view.Content, "\n")
		if len(lines) > h {
			out = append(out, problem{CheckRender, CheckRender + ": " + at + " too many lines", fmt.Sprintf("%d lines at %s", len(lines), at)})
		}
		for i, line := range lines {
			if cw := ansi.StringWidth(line); cw > w {
				out = append(out, problem{CheckRender, CheckRender + ": " + at + " line wider than the width", fmt.Sprintf("line %d is %d cells at %s: %q", i+1, cw, at, ansi.Strip(line))})
				break
			}
		}
		if c := view.Cursor; c != nil && (c.X < 0 || c.Y < 0 || c.X >= w || c.Y >= h) {
			out = append(out, problem{CheckRender, CheckRender + ": " + at + " cursor outside the screen", fmt.Sprintf("cursor %d,%d at %s", c.X, c.Y, at)})
		}
	}
	return out
}

func sizeText(w, h int) string { return fmt.Sprintf("%dx%d", w, h) }

// refusal reports an operation error that is a refusal, not a finding: a
// verb guarded off where it acts, markdown's ErrRefused ("not toggled"),
// or what the subject expects.
func refusal(s *Subject, op, err string) bool {
	if strings.Contains(err, "is not applicable now") || strings.Contains(err, "not toggled") {
		return true
	}
	return s.Expected != nil && s.Expected(op, err)
}

// normalizeError strips the volatile parts of an error (quoted ids,
// numbers) so errors of one kind group together.
func normalizeError(err string) string {
	var b strings.Builder
	quoted := false
	for _, r := range firstLine(err) {
		switch {
		case r == '"':
			if !quoted {
				b.WriteString(`"…"`)
			}
			quoted = !quoted
		case quoted:
		case r >= '0' && r <= '9':
			if s := b.String(); !strings.HasSuffix(s, "#") {
				b.WriteRune('#')
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// actionProblems are what one action did wrong: a panic, an operation
// error, a chain of commands that never settled.
func actionProblems(ss *session, action string) []problem {
	var out []problem
	if ss.panic != nil {
		out = append(out, problem{CheckPanic, CheckPanic + ": " + ss.panic.Where + ": " + firstLine(ss.panic.Value), ss.panic.Value + "\n" + ss.panic.Stack})
		return out
	}
	if ss.overrun {
		out = append(out, problem{CheckSettle, CheckSettle + ": more than " + fmt.Sprint(maxMessages) + " messages", "after " + action})
	}
	for _, h := range ss.ran {
		if h.Result.OK || h.Depth > 0 {
			continue
		}
		// A wrapper (macro.record re-dispatching the key) fails with the
		// error of the request it ran: the finding is that request's.
		op, depth := h.Request.Key(), 0
		for _, inner := range ss.ran {
			if inner.Depth > depth && !inner.Result.OK && inner.Result.Error == h.Result.Error {
				op, depth = inner.Request.Key(), inner.Depth
			}
		}
		if refusal(ss.s, op, h.Result.Error) {
			continue
		}
		out = append(out, problem{CheckErrors, CheckErrors + ": " + op + ": " + normalizeError(h.Result.Error), op + ": " + h.Result.Error})
	}
	return out
}

// staticTraps reads every registered mode's rows and reports the modes no
// row leaves: no row in the mode (or the any-mode table, for a base mode)
// runs mode.leave, mode.set, an exit verb (exitVerbs), or enters a
// pending mode that has one (a passthrough mode's leader). A
// pending mode is one-shot (any answering key consumes it), so it needs
// only some row. A handler that changes the mode cannot be read
// statically; such a mode is listed with the note "exit only by handler".
func staticTraps(r *ochazuke.Runtime) []problem {
	modes := sortedKeys(r.Modes)
	var out []problem
	rowsIn := func(mode string) []ochazuke.Binding {
		var rows []ochazuke.Binding
		for _, name := range sortedKeys(r.Nouns) {
			rows = append(rows, modeRows(r.Nouns[name], mode)...)
		}
		for _, id := range sortedKeys(r.Instances) {
			for _, b := range r.Instances[id].Bindings {
				if b.Mode == mode {
					rows = append(rows, b)
				}
			}
		}
		return rows
	}
	for _, mode := range r.PendingModes() {
		if len(rowsIn(mode)) == 0 {
			out = append(out, problem{CheckStaticTrap, CheckStaticTrap + ": pending mode " + mode + " has no rows", "no row answers in " + mode + ", so no key consumes it"})
		}
	}
	for _, mode := range modes {
		if mode == "normal" {
			continue
		}
		// The any-mode rows that can answer in the mode: app's and the
		// modal noun's, and those of nouns with rows in the mode (the
		// editor's only answer with an input focused, so they count only
		// when the editor has rows in it).
		rows := rowsIn(mode)
		for _, name := range sortedKeys(r.Nouns) {
			if name == "app" || name == "modal" || len(modeRows(r.Nouns[name], mode)) > 0 {
				rows = append(rows, r.Nouns[name].Bindings[""]...)
			}
		}
		if !slices.ContainsFunc(rows, leavesIn(r, rowsIn)) {
			out = append(out, problem{CheckStaticTrap, CheckStaticTrap + ": mode " + mode + " has no exit row", "no row in " + mode + " (or any mode) runs a verb that leaves it; exit only by handler, if at all"})
		}
	}
	return out
}

var exitVerbs = []string{"leave", "pop", "close", "cancel", "detach", "commit", "pick", "accept", "keep", "clear", "choose"}

// leaves reports whether a row leaves the mode it answers in: an exit verb
// (or one ending "-<exit>": list.filter-clear), mode.set, or mode.enter of
// a pending mode one of whose rows leaves (the terminal leader's detach).
func leavesIn(r *ochazuke.Runtime, rowsIn func(string) []ochazuke.Binding) func(ochazuke.Binding) bool {
	var leaves func(b ochazuke.Binding, depth int) bool
	leaves = func(b ochazuke.Binding, depth int) bool {
		if b.Unbind {
			return false
		}
		verb := b.Request.Verb
		if b.Request.Noun == "mode" && (verb == "set" || verb == "leave") {
			return true
		}
		for _, x := range exitVerbs {
			if verb == x || strings.HasSuffix(verb, "-"+x) {
				return true
			}
		}
		if b.Request.Key() == "mode.enter" && depth < 3 {
			if m, _ := b.Request.Args["mode"].(string); m != "" && r.IsPendingMode(m) {
				return slices.ContainsFunc(rowsIn(m), func(x ochazuke.Binding) bool { return leaves(x, depth+1) })
			}
		}
		return false
	}
	return func(b ochazuke.Binding) bool { return leaves(b, 0) }
}

// modeRows is a noun's rows in a mode and in the modes it extends
// (Noun.Extends), as the walk reads them.
func modeRows(n *ochazuke.Noun, mode string) []ochazuke.Binding {
	var out []ochazuke.Binding
	seen := map[string]bool{}
	for mode != "" && !seen[mode] {
		seen[mode] = true
		out = append(out, n.Bindings[mode]...)
		mode = n.Extends[mode]
	}
	return out
}
