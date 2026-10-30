package explore

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Report is what a run found.
type Report struct {
	Subject string         `json:"subject"`
	Budget  Budget         `json:"budget"`
	States  map[string]int `json:"states"` // states reached by key mode ("abstract", "full")
	Steps   int            `json:"steps"`  // actions played by the search
	Replays int            `json:"replays"`
	// Truncated is true when the step budget ended the search before the
	// depth did.
	Truncated bool          `json:"truncated"`
	Elapsed   time.Duration `json:"-"`
	StateKey  string        `json:"stateKey"` // what a state is keyed by
	Ignores   string        `json:"ignores"`  // what the key leaves out
	Findings  []*Finding    `json:"findings"`
	// Errors are the explorer's own failures (a replay that did not reach
	// the state it reached before): a bug in the explorer or a subject
	// that is not deterministic, never an application finding.
	Errors   []string `json:"errors,omitempty"`
	Coverage Coverage `json:"coverage"`
}

// Finding is one problem, by the shortest key path the search found to
// it (shrunk by delta debugging when Shrunk).
type Finding struct {
	Check   string   `json:"check"`
	Summary string   `json:"summary"`
	Detail  string   `json:"detail"`
	Path    []string `json:"path"`
	Found   int      `json:"found"`  // the length of the path the search found
	Shrunk  bool     `json:"shrunk"` // Path is shorter than the one found
	Count   int      `json:"count"`  // how many times the search met it
	action  bool
	sig     string
}

// Coverage is what the search reached and what it never did.
type Coverage struct {
	NounModes        map[string]int `json:"nounModes"` // "noun / mode" of the focus, states per pair
	ModesNever       []string       `json:"modesNever"`
	OperationsNever  []string       `json:"operationsNever"`
	Operations       int            `json:"operations"`
	RowsNever        []string       `json:"rowsNever"` // static noun rows no pressed key answered with
	Rows             int            `json:"rows"`      // static noun rows in all
	GuardsNeverTrue  []string       `json:"guardsNeverTrue"`
	GuardsNeverFalse []string       `json:"guardsNeverFalse"`
	// Timers are the timer commands (tea.Tick, tea.Every) the driver
	// dropped, by closure: the framework's own fire at Wait through their
	// tick accessors; an application's own never do.
	Timers map[string]int `json:"timers,omitempty"`
	// Blocked are commands still running when the driver stopped waiting
	// for them (a stream read, a job waiting on a fake), by closure.
	Blocked     map[string]int `json:"blocked,omitempty"`
	LongRunning []string       `json:"longRunning,omitempty"` // jobs still running after a wait, with a command in flight
	Trapped     int            `json:"trapped"`
	Unproven    int            `json:"unproven"` // states whose way home the trap search ran out of steps finding
}

// StateKeyDoc is what the state key holds, as the report prints it.
const StateKeyDoc = "page; focus path (instance id and noun, a list row as its noun and list); mode name with payload parts (count, opcount, op, shadow, request) and sequence prefix; open modal nouns in order; every guard evaluated at the focus; a hash of the rows that answer at the focus in the mode; a focused list's position (empty/only/first/middle/last) with filter, marks and visual; a focused input's cursor class (empty/start/middle/end); macro recording and an active register; Full adds a hash of the 80x24 screen"

// IgnoresDoc is what the state key leaves out, so two states it merges
// may still differ there.
const IgnoresDoc = "list row ids and how many rows; scroll offsets and the page's scroll; text in inputs (only its cursor class); register and yank contents; notices and the status line; the jobs list and the ticker; table column offsets and widths; settings values; detail scroll; the terminal size (unless Full); application state the runtime does not hold; pending commands in flight"

func (x *explorer) sortedFindings() []*Finding {
	out := make([]*Finding, 0, len(x.findings))
	for _, k := range sortedKeys(x.findings) {
		out = append(out, x.findings[k])
	}
	slices.SortStableFunc(out, func(a, b *Finding) int {
		if a.Check != b.Check {
			return strings.Compare(a.Check, b.Check)
		}
		return strings.Compare(a.Summary, b.Summary)
	})
	return out
}

func (x *explorer) report() Report {
	b := x.b
	b.Observe = nil
	rep := Report{
		Subject: x.s.Name, Budget: b, States: map[string]int{x.mode.String(): len(x.nodes)},
		Steps: x.steps, Replays: int(x.replays.Load()), Truncated: x.truncated,
		StateKey: StateKeyDoc, Ignores: IgnoresDoc, Findings: x.sortedFindings(), Errors: x.errs,
	}
	c := Coverage{NounModes: x.nounMode, Rows: len(x.staticRows), Operations: len(x.ops), Timers: x.timers, Blocked: x.blocked, Trapped: x.trapped, Unproven: x.unproven}
	for _, m := range x.modes {
		if !x.modesSeen[m] {
			c.ModesNever = append(c.ModesNever, m)
		}
	}
	for _, op := range x.ops {
		if !x.opsRun[op] {
			c.OperationsNever = append(c.OperationsNever, op)
		}
	}
	for _, row := range x.staticRows {
		if !x.answered[row] {
			c.RowsNever = append(c.RowsNever, row)
		}
	}
	names := map[string]bool{}
	for g := range x.guardTrue {
		names[g] = true
	}
	for g := range x.guardFalse {
		names[g] = true
	}
	for _, g := range sortedKeys(names) {
		if !x.guardTrue[g] {
			c.GuardsNeverTrue = append(c.GuardsNeverTrue, g)
		}
		if !x.guardFalse[g] {
			c.GuardsNeverFalse = append(c.GuardsNeverFalse, g)
		}
	}
	c.LongRunning = sortedKeys(x.longRunning)
	rep.Coverage = c
	return rep
}

// JSON is the report as indented JSON (without Elapsed, so it compares).
func (r Report) JSON() []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}

// ByCheck groups the findings by check, in report order.
func (r Report) ByCheck() map[string][]*Finding {
	out := map[string][]*Finding{}
	for _, f := range r.Findings {
		out[f.Check] = append(out[f.Check], f)
	}
	return out
}

// Text is the report for a terminal: counts, findings by check (the first
// top of each), coverage. top <= 0 lists every finding.
func (r Report) Text(top int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "explore %s: depth %d, steps %d/%d, replays %d", r.Subject, r.Budget.Depth, r.Steps, r.Budget.Steps, r.Replays)
	if r.Truncated {
		b.WriteString(" (step budget reached)")
	}
	if r.Elapsed > 0 {
		fmt.Fprintf(&b, ", %s", r.Elapsed.Round(time.Millisecond))
	}
	b.WriteString("\n  states:")
	for _, k := range sortedKeys(r.States) {
		fmt.Fprintf(&b, " %s %d", k, r.States[k])
	}
	b.WriteString("\n")
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  EXPLORER ERROR: %s\n", e)
	}
	groups := r.ByCheck()
	if len(groups) == 0 {
		b.WriteString("  no findings\n")
	}
	for _, check := range sortedKeys(groups) {
		fs := groups[check]
		fmt.Fprintf(&b, "  %s: %d\n", check, len(fs))
		for i, f := range fs {
			if top > 0 && i >= top {
				fmt.Fprintf(&b, "    … %d more\n", len(fs)-top)
				break
			}
			shrunk := ""
			if f.Shrunk {
				shrunk = fmt.Sprintf(" (shrunk from %d)", f.Found)
			}
			fmt.Fprintf(&b, "    - %s ×%d\n      keys: %s%s\n", f.Summary, f.Count, pathText(f.Path), shrunk)
			if d := firstLine(f.Detail); d != "" && d != f.Summary {
				fmt.Fprintf(&b, "      %s\n", d)
			}
		}
	}
	c := r.Coverage
	fmt.Fprintf(&b, "  coverage: %d noun/mode pairs; modes never entered %v; %d/%d operations never run; %d/%d rows never answered; guards never true %v; never false %v; trapped %d, unproven %d\n",
		len(c.NounModes), c.ModesNever, len(c.OperationsNever), c.Operations, len(c.RowsNever), c.Rows, c.GuardsNeverTrue, c.GuardsNeverFalse, c.Trapped, c.Unproven)
	if len(c.LongRunning) > 0 {
		fmt.Fprintf(&b, "  long-running jobs (not failures): %v\n", c.LongRunning)
	}
	if len(c.Blocked) > 0 {
		fmt.Fprintf(&b, "  commands left running past the window: %v\n", c.Blocked)
	}
	if len(c.Timers) > 0 {
		fmt.Fprintf(&b, "  timer commands dropped (the framework's are delivered by <wait>): %v\n", c.Timers)
	}
	return b.String()
}

// Legend is what the state key holds and ignores, printed once under one
// or more reports.
func (r Report) Legend() string {
	return fmt.Sprintf("state key: %s\nthe key ignores: %s\n", r.StateKey, r.Ignores)
}

// GoTests is a Go test per finding (static traps have no path to replay),
// for a file that defines subject as the Go expression of the Subject.
func (r Report) GoTests(subject string) string {
	var b strings.Builder
	seen := map[string]int{}
	for _, f := range r.Findings {
		if f.Check == CheckStaticTrap {
			continue
		}
		test := f.GoTest(subject)
		name := testName(f.Check + " " + f.Summary)
		if seen[name]++; seen[name] > 1 {
			test = strings.Replace(test, "func "+name+"(", fmt.Sprintf("func %s%d(", name, seen[name]), 1)
		}
		b.WriteString(test + "\n")
	}
	return b.String()
}

// GoTest is a Go test that replays the finding's path and fails while it
// still shows; subject is the Go expression of the Subject.
func (f *Finding) GoTest(subject string) string {
	quoted := make([]string, len(f.Path))
	for i, k := range f.Path {
		quoted[i] = strconv.Quote(k)
	}
	return fmt.Sprintf(`// %s: %s
func %s(t *testing.T) {
	if err := explore.Reproduce(%s, %q, []string{%s}); err != nil {
		t.Fatal(err)
	}
}
`, f.Check, f.Summary, testName(f.Check+" "+f.Summary), subject, f.Check, strings.Join(quoted, ", "))
}

func testName(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			up = true
			continue
		}
		if b.Len() >= 48 {
			break
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return "TestExplore" + b.String()
}

// Reproduce replays path on the subject and returns an error naming every
// problem of check that shows: after the path's last action, or in the
// state it ends in. Nil means the path no longer shows it.
func Reproduce(s Subject, check string, path []string) error {
	sizes := s.Sizes
	if len(sizes) == 0 {
		sizes = DefaultSizes
	}
	x := &explorer{s: &s, b: Budget{}.withDefaults(), sizes: sizes, answered: map[string]bool{}, opsRun: map[string]bool{}, timers: map[string]int{}, blocked: map[string]int{}, longRunning: map[string]bool{}}
	var found []string
	if len(path) > 0 {
		ss, err := x.replay(path[:len(path)-1])
		if err != nil {
			return err
		}
		if ss.panic == nil && !ss.quit {
			st := stateOf(ss)
			hints, offered := hintsAndRows(ss.m.Runtime)
			n := &node{abstract: st.Key(), hints: hints, offered: offered}
			ss.act(path[len(path)-1])
			for _, p := range x.evalAction(ss, n, path[len(path)-1], false) {
				if p.Check == check {
					found = append(found, p.Detail)
				}
			}
		}
		ss.close()
	}
	ss, err := x.replay(path)
	if err != nil {
		return err
	}
	defer ss.close()
	for _, p := range append(stateProblems(ss), renderProblems(ss, sizes)...) {
		if p.Check == check {
			found = append(found, p.Detail)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("%s after %s:\n%s", check, pathText(path), strings.Join(found, "\n"))
}
