package explore

import (
	"slices"
)

// shrinkBudget bounds the replays spent shrinking one finding.
const shrinkBudget = 96

// shrinkAll shrinks the first ShrinkPerCheck findings of each check (in
// report order) by delta debugging.
func (x *explorer) shrinkAll() {
	per := map[string]int{}
	for _, f := range x.sortedFindings() {
		if f.Check == CheckTrap || f.Check == CheckStaticTrap || len(f.Path) < 2 {
			continue
		}
		if per[f.Check] >= x.b.ShrinkPerCheck {
			continue
		}
		per[f.Check]++
		if p := x.shrink(f); len(p) < len(f.Path) {
			f.Path, f.Shrunk = p, true
		}
	}
}

// fails reports whether path still shows the finding's problem: after its
// last action for an action finding, in the state it ends in otherwise.
func (x *explorer) fails(f *Finding, path []string) bool {
	if f.action {
		if len(path) == 0 {
			return false
		}
		ss, err := x.replay(path[:len(path)-1])
		if err != nil || ss.panic != nil || ss.quit {
			ss.close()
			return false
		}
		defer ss.close()
		st := stateOf(ss)
		hints, offered := hintsAndRows(ss.m.Runtime)
		n := &node{abstract: st.Key(), hints: hints, offered: offered}
		ss.act(path[len(path)-1])
		probs := x.evalAction(ss, n, path[len(path)-1], false)
		return slices.ContainsFunc(probs, func(p problem) bool { return p.Sig == f.sig })
	}
	ss, err := x.replay(path)
	if err != nil {
		return false
	}
	defer ss.close()
	probs := stateProblems(ss)
	probs = append(probs, renderProblems(ss, x.sizes)...)
	return slices.ContainsFunc(probs, func(p problem) bool { return p.Sig == f.sig })
}

// shrink is ddmin over the path (the last action of an action finding
// stays): it removes chunks while the problem still shows.
func (x *explorer) shrink(f *Finding) []string {
	path := slices.Clone(f.Path)
	var last []string
	if f.action {
		last = path[len(path)-1:]
		path = path[:len(path)-1]
	}
	try := func(p []string) bool { return x.fails(f, append(slices.Clone(p), last...)) }
	tries := 0
	n := 2
	for len(path) >= 1 && tries < shrinkBudget {
		chunk := max(1, (len(path)+n-1)/n)
		reduced := false
		for start := 0; start < len(path) && tries < shrinkBudget; start += chunk {
			candidate := append(slices.Clone(path[:start]), path[min(len(path), start+chunk):]...)
			tries++
			if try(candidate) {
				path, reduced = candidate, true
				n = max(n-1, 2)
				break
			}
		}
		if !reduced {
			if chunk == 1 {
				break
			}
			n = min(n*2, len(path))
		}
	}
	return append(path, last...)
}
