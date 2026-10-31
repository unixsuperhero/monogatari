package monogatari

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Minimum is what a set of stories exercises: per mode and noun the
// noun.verb requests their keys resolved to (the minimum a component needs
// to be full-featured; which keys reach them is the app's choice), and the
// edges between modes with what took them.
type Minimum struct {
	Verbs map[string]map[string][]string // mode -> noun -> verbs
	Edges []Edge
}

// Edge is a mode change one step made.
type Edge struct {
	From, To string
	Via      string // noun.verb (joined when a step ran several), or the row/tick that did it
}

// Derive collects the minimum from story traces.
func Derive(traces ...[]Step) Minimum {
	m := Minimum{Verbs: map[string]map[string][]string{}}
	seen := map[Edge]bool{}
	for _, trace := range traces {
		for _, st := range trace {
			var via []string
			for _, r := range st.Ran {
				if !r.OK {
					continue
				}
				noun, _ := ochazuke.SplitKey(r.Verb)
				if m.Verbs[r.Mode] == nil {
					m.Verbs[r.Mode] = map[string][]string{}
				}
				if !slices.Contains(m.Verbs[r.Mode][noun], r.Verb) {
					m.Verbs[r.Mode][noun] = append(m.Verbs[r.Mode][noun], r.Verb)
				}
				via = append(via, r.Verb)
			}
			if st.Before == st.After {
				continue
			}
			e := Edge{From: st.Before, To: st.After, Via: strings.Join(via, " + ")}
			if e.Via == "" {
				switch {
				case st.Event.Tick != "":
					e.Via = "tick " + st.Event.Tick
				case st.Event.Job != "":
					e.Via = "job " + st.Event.Job
				default:
					e.Via = "row " + st.Event.String()
				}
			}
			if !seen[e] {
				seen[e] = true
				m.Edges = append(m.Edges, e)
			}
		}
	}
	for _, nouns := range m.Verbs {
		for _, verbs := range nouns {
			slices.Sort(verbs)
		}
	}
	slices.SortFunc(m.Edges, func(a, b Edge) int {
		return strings.Compare(a.From+"\x00"+a.To+"\x00"+a.Via, b.From+"\x00"+b.To+"\x00"+b.Via)
	})
	return m
}

// Trapped lists the modes the stories enter from which no chain of edges
// leads back to normal: each needs an exit (terminal a detach, insert an
// esc or cancel, a pending mode a timeout or esc).
func (m Minimum) Trapped() []string {
	out := map[string][]string{}
	entered := map[string]bool{}
	for _, e := range m.Edges {
		out[e.From] = append(out[e.From], e.To)
		entered[e.To] = true
	}
	var trapped []string
	for mode := range entered {
		if mode == "normal" {
			continue
		}
		seen, queue, ok := map[string]bool{mode: true}, []string{mode}, false
		for len(queue) > 0 && !ok {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range out[cur] {
				if next == "normal" {
					ok = true
				}
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		if !ok {
			trapped = append(trapped, mode)
		}
	}
	slices.Sort(trapped)
	return trapped
}

// Markdown renders the minimum as the README's tables.
func (m Minimum) Markdown() string {
	var b strings.Builder
	b.WriteString("| mode | noun | verbs |\n|---|---|---|\n")
	modes := sortedKeys(m.Verbs)
	for _, mode := range modes {
		for _, noun := range sortedKeys(m.Verbs[mode]) {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mode, noun, "`"+strings.Join(m.Verbs[mode][noun], "` `")+"`")
		}
	}
	b.WriteString("\nMode edges (from → to via):\n\n")
	for _, e := range m.Edges {
		fmt.Fprintf(&b, "- `%s` → `%s` via `%s`\n", e.From, e.To, e.Via)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
