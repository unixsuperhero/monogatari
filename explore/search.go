package explore

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// node is one state the search reached, by the shortest path found.
type node struct {
	st       State
	key      string
	abstract string // the abstract key, to verify a replay
	path     []string
	actions  []string
	hints    map[string]ochazuke.Binding // first hint row per key it is pressed with
	offered  map[string][]string         // action -> identities of the effective rows it presses
	edges    map[string]string           // action -> child key
	expanded bool
	fork     *forked
}

type forked struct {
	m             *ochazuke.Model
	now           time.Time
	width, height int
}

type explorer struct {
	s     *Subject
	b     Budget
	mode  KeyMode
	sizes [][2]int

	nodes       map[string]*node
	order       []*node
	steps       int
	replays     atomic.Int64
	workers     int
	truncated   bool
	initialPage string

	findings map[string]*Finding
	errs     []string // explorer bugs: a replay that did not reach its state

	nounMode    map[string]int
	modesSeen   map[string]bool
	guardTrue   map[string]bool
	guardFalse  map[string]bool
	answered    map[string]bool
	staticRows  []string
	opsRun      map[string]bool
	ops         []string
	modes       []string
	timers      map[string]int
	blocked     map[string]int
	longRunning map[string]bool
	trapped     int
	unproven    int

	seq   int        // steps told to Budget.Observe
	fresh []*Finding // findings recorded since the last observed step
}

// Run explores a subject within a budget and reports what it found. The
// same subject and budget give the same report (Elapsed aside).
func Run(s Subject, b Budget) Report {
	b = b.withDefaults()
	began := time.Now()
	sizes := s.Sizes
	if len(sizes) == 0 {
		sizes = DefaultSizes
	}
	x := search(&s, b, b.Key, sizes)
	rep := x.report()
	if b.CompareFull {
		other := Abstract
		if b.Key == Abstract {
			other = Full
		}
		quiet := b
		quiet.Observe = nil
		y := search(&s, quiet, other, sizes)
		rep.States[other.String()] = len(y.nodes)
	}
	rep.Elapsed = time.Since(began)
	return rep
}

func search(s *Subject, b Budget, mode KeyMode, sizes [][2]int) *explorer {
	x := &explorer{
		s: s, b: b, mode: mode, sizes: sizes,
		nodes: map[string]*node{}, findings: map[string]*Finding{}, workers: runtime.GOMAXPROCS(0),
		nounMode: map[string]int{}, modesSeen: map[string]bool{}, guardTrue: map[string]bool{}, guardFalse: map[string]bool{},
		answered: map[string]bool{}, opsRun: map[string]bool{}, timers: map[string]int{}, blocked: map[string]int{}, longRunning: map[string]bool{},
	}
	if s.Serial {
		x.workers = 1
	}
	ss, err := x.replay(nil)
	if err != nil {
		x.errs = append(x.errs, "New: "+err.Error())
		return x
	}
	if ss.panic != nil {
		x.record(problem{CheckPanic, CheckPanic + ": " + ss.panic.Where + ": " + firstLine(ss.panic.Value), ss.panic.Value + "\n" + ss.panic.Stack}, nil, false)
		return x
	}
	r := ss.m.Runtime
	x.initialPage = r.Page
	x.ops = sortedKeys(r.Operations)
	x.modes = append(sortedKeys(r.Modes), r.PendingModes()...)
	slices.Sort(x.modes)
	x.staticRows = staticRowIDs(r)
	for _, p := range staticTraps(r) {
		x.record(p, nil, false)
	}
	root := x.discover(ss, nil)
	queue := []*node{root}
	x.observe(PhaseStart, ss, nil, root.key, true)
	ss.close()
	for _, seed := range s.Seeds {
		ss, err := x.replay(seed)
		if err != nil {
			x.errs = append(x.errs, "seed: "+err.Error())
			continue
		}
		known := len(x.nodes)
		n := x.discover(ss, slices.Clone(seed))
		if n != nil && !n.expanded && len(n.path) == len(seed) {
			queue = append(queue, n)
		}
		x.observe(PhaseStart, ss, seed, n.key, len(x.nodes) > known)
		ss.close()
	}
	for len(queue) > 0 && !x.truncated {
		n := queue[0]
		queue = queue[1:]
		if n == nil || n.expanded || n.st.End != "" || len(n.path) >= b.Depth {
			continue
		}
		queue = append(queue, x.expand(n)...)
	}
	x.traps()
	x.observe(PhaseTraps, nil, nil, "", false)
	if !b.NoShrink {
		x.shrinkAll()
	}
	return x
}

// replay plays a path on a fresh session.
func (x *explorer) replay(path []string) (*session, error) {
	x.replays.Add(1)
	ss, err := start(x.s)
	if err != nil || ss.panic != nil {
		return ss, err
	}
	for _, a := range path {
		if ss.panic != nil || ss.quit {
			break
		}
		ss.act(a)
	}
	return ss, nil
}

// at is a session standing in n: a fork of its model when the subject
// forks, else a replay of its path.
func (x *explorer) at(n *node) (*session, error) {
	if n.fork != nil {
		m, err := x.s.Fork(n.fork.m)
		if err != nil {
			return nil, err
		}
		ss := &session{s: x.s, m: m, now: n.fork.now, width: n.fork.width, height: n.fork.height}
		ss.wrap()
		m.Now = func() time.Time { return ss.now }
		ss.markHistory()
		return ss, nil
	}
	return x.replay(n.path)
}

// keyOf is the state of a session under the explorer's key mode, and the
// abstract key beside it. In Full mode the screen is drawn at 80x24, so it
// is read after everything else the session is asked.
func (x *explorer) keyOf(ss *session, st State) (State, string) {
	abstract := st.Key()
	if x.mode == Full && st.End == "" {
		view, err := ss.render(80, 24)
		if err == nil {
			st.Screen = shortHash(view.Content)
		}
	}
	return st, abstract
}

// discover registers the state a session stands in, reached by path, and
// runs the state checks when it is new. It returns the node, new or not.
func (x *explorer) discover(ss *session, path []string) *node {
	st := stateOf(ss)
	var actions []string
	var hints map[string]ochazuke.Binding
	var offered map[string][]string
	if st.End == "" {
		r := ss.m.Runtime
		actions = actionsOf(r, x.sizes)
		hints, offered = hintsAndRows(r)
	}
	st, abstract := x.keyOf(ss, st)
	key := st.Key()
	if n := x.nodes[key]; n != nil {
		return n
	}
	n := &node{st: st, key: key, abstract: abstract, path: path, actions: actions, hints: hints, offered: offered, edges: map[string]string{}}
	x.nodes[key] = n
	x.order = append(x.order, n)
	if st.End != "" {
		return n
	}
	x.cover(ss, st)
	if x.s.Fork != nil {
		if m, err := x.s.Fork(ss.m); err == nil {
			n.fork = &forked{m: m, now: ss.now, width: ss.width, height: ss.height}
		}
	}
	for _, p := range stateProblems(ss) {
		x.record(p, path, false)
	}
	for _, p := range renderProblems(ss, x.sizes) {
		x.record(p, path, false)
	}
	return n
}

// hintsAndRows reads the footer's hint rows by the key each is pressed
// with, and the effective rows by the same.
func hintsAndRows(r *ochazuke.Runtime) (map[string]ochazuke.Binding, map[string][]string) {
	prefix := r.Mode.Payload.Prefix
	hints := map[string]ochazuke.Binding{}
	for _, b := range ochazuke.HintBindings(r, r.Focus) {
		if k := nextKey(b, prefix); k != "" {
			if _, ok := hints[k]; !ok {
				hints[k] = b
			}
		}
	}
	offered := map[string][]string{}
	for _, b := range r.ListBindings(r.Focus) {
		if k := nextKey(b, prefix); k != "" {
			offered[k] = append(offered[k], rowID(b))
		}
	}
	return hints, offered
}

func rowID(b ochazuke.Binding) string {
	who := b.Noun
	if b.Instance != "" {
		who = "#" + b.Instance
	}
	keys := b.Keys
	if keys == "" {
		keys = "<" + b.Pattern + ">"
	}
	mode := b.Mode
	if mode == "" {
		mode = "*"
	}
	when := ""
	if b.When != "" {
		when = " when " + b.When
	}
	return fmt.Sprintf("%s %s %s → %s%s", who, mode, keys, requestText(b.Request), when)
}

func requestText(q ochazuke.Request) string {
	if q.Verb == "" && q.Noun == "" {
		return "(transition)"
	}
	return q.Key()
}

func staticRowIDs(r *ochazuke.Runtime) []string {
	seen := map[string]bool{}
	for _, name := range sortedKeys(r.Nouns) {
		noun := r.Nouns[name]
		for _, mode := range sortedKeys(noun.Bindings) {
			for _, b := range noun.Bindings[mode] {
				if b.Unbind {
					continue
				}
				b.Noun, b.Instance, b.Mode = name, "", mode
				seen[rowID(b)] = true
			}
		}
	}
	return sortedKeys(seen)
}

// cover adds a new state to the coverage counts.
func (x *explorer) cover(ss *session, st State) {
	r := ss.m.Runtime
	noun := ""
	if inst := r.Instances[r.Focus]; inst != nil {
		noun = inst.Noun
	}
	x.nounMode[noun+" / "+r.Mode.Name]++
	x.modesSeen[r.Mode.Name] = true
	for _, g := range st.Guards {
		if name, off := strings.CutPrefix(g, "!"); off {
			x.guardFalse[name] = true
		} else {
			x.guardTrue[g] = true
		}
	}
}

// expand plays every action of n and returns the new states.
func (x *explorer) expand(n *node) []*node {
	n.expanded = true
	actions := n.actions
	if left := x.b.Steps - x.steps; len(actions) > left {
		actions = actions[:max(0, left)]
		x.truncated = true
		n.expanded = false
	}
	type played struct {
		ss  *session
		err error
		bad bool // the replay did not reach n
	}
	results := make([]played, len(actions))
	workers := max(1, min(x.workers, len(actions)))
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				ss, err := x.at(n)
				if err != nil {
					results[i] = played{err: err}
					continue
				}
				if n.fork == nil && ss.panic == nil && !ss.quit && stateOf(ss).Key() != n.abstract {
					ss.close()
					results[i] = played{bad: true}
					continue
				}
				ss.act(actions[i])
				results[i] = played{ss: ss}
			}
		}()
	}
	for i := range actions {
		next <- i
	}
	close(next)
	wg.Wait()
	var out []*node
	for i, a := range actions {
		res := results[i]
		switch {
		case res.err != nil:
			x.errs = append(x.errs, "replay "+pathText(n.path)+": "+res.err.Error())
			continue
		case res.bad:
			x.errs = append(x.errs, "nondeterministic replay of "+pathText(n.path))
			continue
		}
		ss := res.ss
		x.steps++
		path := append(slices.Clone(n.path), a)
		for _, p := range x.evalAction(ss, n, a, true) {
			x.record(p, path, true)
		}
		known := len(x.nodes)
		child := x.discover(ss, path)
		n.edges[a] = child.key
		if len(child.path) == len(path) && slices.Equal(child.path, path) {
			out = append(out, child)
		}
		x.observe(PhaseSearch, ss, path, child.key, len(x.nodes) > known)
		ss.close()
	}
	return out
}

// evalAction judges one action played in n: its problems, the footer's
// promise for its key, the jobs left after a wait. It also counts what
// ran.
func (x *explorer) evalAction(ss *session, n *node, a string, count bool) []problem {
	probs := actionProblems(ss, a)
	ranOps := map[string]bool{}
	for _, h := range ss.ran {
		if count {
			x.opsRun[h.Request.Key()] = true
		}
		if h.Depth == 0 {
			ranOps[h.Request.Key()] = true
		}
	}
	if count {
		for _, t := range ss.timers {
			x.timers[t]++
		}
		for _, b := range ss.newBlocked {
			x.blocked[b]++
		}
	}
	if ss.panic != nil {
		return probs
	}
	changed := stateOf(ss).Key() != n.abstract
	for _, id := range n.offered[a] {
		if !count {
			break
		}
		op := strings.TrimSpace(id[strings.Index(id, "→")+len("→"):])
		op, _, _ = strings.Cut(op, " when ")
		if ranOps[op] || op == "(transition)" && changed {
			x.answered[id] = true
		}
	}
	if b, ok := n.hints[a]; ok && !ss.quit {
		label := rowHintText(b)
		refused, failed := false, ""
		for _, h := range ss.ran {
			if h.Depth == 0 && !h.Result.OK {
				if strings.Contains(h.Result.Error, "is not applicable now") {
					refused = true
				} else if failed == "" && !refusal(x.s, h.Request.Key(), h.Result.Error) {
					failed = h.Result.Error
				}
			}
		}
		switch {
		case refused:
			probs = append(probs, problem{CheckHints, CheckHints + ": " + label + " is refused when pressed", "the footer shows " + label + " but its verb is not applicable"})
		case failed != "":
			probs = append(probs, problem{CheckHints, CheckHints + ": " + label + " fails when pressed: " + normalizeError(failed), "the footer shows " + label + " but pressing it fails: " + failed})
		case len(ss.ran) == 0 && !changed:
			probs = append(probs, problem{CheckHints, CheckHints + ": " + label + " does not answer", "the footer shows " + label + " but pressing " + a + " runs nothing and changes nothing"})
		}
	}
	if a == Wait && !ss.quit {
		for _, j := range ss.m.Runtime.Jobs() {
			if j.State != ochazuke.JobRunning {
				continue
			}
			if len(ss.blocked) == 0 {
				probs = append(probs, problem{CheckJobs, CheckJobs + ": " + j.Verb + " runs with nothing in flight", fmt.Sprintf("job #%d %s (owner %s) is running after a wait, and no command of it is in flight", j.Job, j.Verb, j.Owner)})
			} else if count {
				x.longRunning[j.Verb] = true
			}
		}
	}
	return probs
}

func rowHintText(b ochazuke.Binding) string {
	key := b.Keys
	if key == "" {
		key = "<" + b.Pattern + ">"
	}
	if b.Hint != "" {
		return key + " " + b.Hint
	}
	return key + " " + requestText(b.Request)
}

// record files a problem under its signature, keeping the first (the
// shortest) path found.
func (x *explorer) record(p problem, path []string, action bool) {
	if f := x.findings[p.Sig]; f != nil {
		f.Count++
		return
	}
	f := &Finding{Check: p.Check, Summary: strings.TrimPrefix(p.Sig, p.Check+": "), Detail: p.Detail, Path: slices.Clone(path), Found: len(path), Count: 1, action: action, sig: p.Sig}
	x.findings[p.Sig] = f
	if x.b.Observe != nil {
		x.fresh = append(x.fresh, f)
	}
}

func pathText(path []string) string {
	if len(path) == 0 {
		return "(start)"
	}
	return strings.Join(path, " ")
}

// traps finds the states that cannot get home: the explored graph proves
// most of them can; each one it does not is searched from (a bounded BFS
// of its own); one with no way home within the budget's depth is trapped,
// one whose search ran out of steps is unproven.
func (x *explorer) traps() {
	reach := map[string]bool{}
	for _, n := range x.order {
		if n.st.Home(x.initialPage) {
			reach[n.key] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, n := range x.order {
			if reach[n.key] {
				continue
			}
			for _, a := range n.actions {
				if c, ok := n.edges[a]; ok && reach[c] {
					reach[n.key], changed = true, true
					break
				}
			}
		}
	}
	budget := x.b.Steps
	for _, n := range x.order {
		if reach[n.key] || n.st.End != "" {
			continue
		}
		if budget <= 0 {
			x.unproven++
			continue // the trap budget is spent: counted, not reported
		}
		found, exhausted, used := x.findHome(n, reach, min(budget, x.b.TrapSteps))
		budget -= used
		switch {
		case found:
			reach[n.key] = true
		case exhausted:
			x.trapped++
			x.record(problem{CheckTrap, CheckTrap + ": no way home from " + trapPlace(n.st), fmt.Sprintf("no key path of at most %d keys from %s reaches the initial page in normal mode with no modal", x.b.Depth, n.st)}, n.path, false)
		default:
			x.unproven++
			x.record(problem{CheckTrap, CheckTrap + ": no way home found from " + trapPlace(n.st), fmt.Sprintf("a search of %d steps from %s found no key path to the initial page in normal mode with no modal (not proven: the search ran out of steps)", x.b.TrapSteps, n.st)}, n.path, false)
		}
	}
}

func trapPlace(st State) string {
	focus := ""
	if len(st.Focus) > 0 {
		focus = st.Focus[0]
		if _, noun, ok := strings.Cut(focus, ":"); ok && !strings.HasPrefix(focus, "row:") {
			focus = noun
		}
	}
	place := "mode " + st.Mode + " on " + focus
	if len(st.Modals) > 0 {
		place += " in " + strings.Join(st.Modals, ">")
	}
	return place
}

// findHome searches from n for a state that is home or known to reach it.
func (x *explorer) findHome(n *node, reach map[string]bool, steps int) (found, exhausted bool, used int) {
	type item struct {
		path    []string
		actions []string
	}
	seen := map[string]bool{n.key: true}
	frontier := []item{{nil, homeFirst(n.actions)}}
	for len(frontier) > 0 {
		it := frontier[0]
		frontier = frontier[1:]
		for _, a := range it.actions {
			if used >= steps {
				return false, false, used
			}
			used++
			local := append(slices.Clone(it.path), a)
			ss, err := x.replay(append(slices.Clone(n.path), local...))
			if err != nil {
				return false, false, used
			}
			st := stateOf(ss)
			var acts []string
			if st.End == "" {
				acts = actionsOf(ss.m.Runtime, x.sizes)
			}
			st, _ = x.keyOf(ss, st)
			ss.close()
			key := st.Key()
			if st.Home(x.initialPage) || reach[key] {
				return true, false, used
			}
			if seen[key] || st.End != "" || len(n.path)+len(local) >= x.b.Depth+len(n.path) {
				continue
			}
			seen[key] = true
			if len(local) < x.b.Depth {
				frontier = append(frontier, item{local, homeFirst(acts)})
			}
		}
	}
	return false, true, used
}

// homeFirst orders actions so the keys that usually lead out come first.
func homeFirst(actions []string) []string {
	first := []string{"esc", "q", "ctrl+a", "enter", "ctrl+g", "backspace"}
	var out []string
	for _, k := range first {
		if slices.Contains(actions, k) {
			out = append(out, k)
		}
	}
	for _, a := range actions {
		if !slices.Contains(first, a) && !strings.HasPrefix(a, "<resize") {
			out = append(out, a)
		}
	}
	return out
}
