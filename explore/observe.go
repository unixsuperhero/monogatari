package explore

import (
	"slices"
)

// Phase is the part of a run a Step reports.
type Phase string

const (
	// PhaseStart is the initial state, or the state a seed reaches.
	PhaseStart Phase = "start"
	// PhaseSearch is one action the search played.
	PhaseSearch Phase = "search"
	// PhaseTraps is the end of the trap search (one step, carrying the
	// trap findings); shrinking follows and reports no steps.
	PhaseTraps Phase = "traps"
)

// Step is what Budget.Observe is told after each move of the search, in
// the order the search makes them: the same subject and budget give the
// same steps. A Step is a view of the run; nothing an observer does with
// it changes what the run finds.
type Step struct {
	Seq    int      // 1, 2, … in order
	Phase  Phase    //
	Path   []string // the key path that reached the state, from the initial state
	Action string   // the last key of Path; "" for a start step
	State  string   // the state key under the budget's key mode
	New    bool     // the first time the run reached this state
	// Visited is the states reached so far, Steps the actions played so
	// far; Budget and Depth are the budget's, defaults applied.
	Visited, Steps, Budget, Depth int
	// Findings are the findings first recorded at this step, as found
	// (Run's report shrinks some of their paths afterwards).
	Findings []Finding
	// Frame draws the state's screen at the session's terminal size. It
	// is valid only during the Observe call and returns "" when the state
	// ended (a quit, a panic) or the step has no session (PhaseTraps).
	// Calling it never changes the run: the session is discarded after.
	Frame func() string
}

// observe tells the budget's observer about a step and clears the
// findings recorded since the last one. ss may be nil.
func (x *explorer) observe(phase Phase, ss *session, path []string, key string, isNew bool) {
	if x.b.Observe == nil {
		x.fresh = nil
		return
	}
	x.seq++
	st := Step{
		Seq: x.seq, Phase: phase, Path: slices.Clone(path), State: key, New: isNew,
		Visited: len(x.nodes), Steps: x.steps, Budget: x.b.Steps, Depth: x.b.Depth,
		Frame: func() string { return "" },
	}
	if phase == PhaseSearch && len(path) > 0 {
		st.Action = path[len(path)-1]
	}
	for _, f := range x.fresh {
		c := *f
		c.Path = slices.Clone(f.Path)
		st.Findings = append(st.Findings, c)
	}
	x.fresh = nil
	if ss != nil {
		st.Frame = func() string { return ss.frame() }
	}
	x.b.Observe(st)
}

// frame is the session's screen at its own size, "" when it ended.
func (ss *session) frame() string {
	if ss == nil || ss.m == nil || ss.panic != nil || ss.quit {
		return ""
	}
	view, err := ss.render(ss.width, ss.height)
	if err != nil {
		return ""
	}
	return view.Content
}

// Frames replays path on the subject and returns its screen at the
// session's size before the first action and after each one (len(path)+1
// frames; an ended session draws ""). It is what a viewer shows to replay
// a finding.
func Frames(s Subject, path []string) ([]string, error) {
	x := &explorer{s: &s}
	ss, err := x.replay(nil)
	if err != nil {
		return nil, err
	}
	defer ss.close()
	out := []string{ss.frame()}
	for _, a := range path {
		if ss.panic == nil && !ss.quit {
			ss.act(a)
		}
		out = append(out, ss.frame())
	}
	return out, nil
}
