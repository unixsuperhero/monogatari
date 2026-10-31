package explore

import (
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// Hostile are the keys pressed in every state whether or not a row
// answers them: the keys a user reaches for when lost. ctrl+c usually
// quits (a "quit" end state, not a failure).
var Hostile = []string{"esc", "enter", "tab", "shift+tab", "backspace", "ctrl+c"}

// classReps are the representatives of a key class, the first one a
// row's exclusions ("alpha-q") leave in.
var classReps = map[string][]string{
	ochazuke.Digit:     {"5", "7", "1"},
	ochazuke.Alpha:     {"a", "x", "b"},
	ochazuke.Upper:     {"A", "X", "B"},
	ochazuke.CtrlAlpha: {"ctrl+o", "ctrl+t", "ctrl+w"},
	ochazuke.Printable: {"x", "y", ";"},
	ochazuke.Paste:     {Paste},
	ochazuke.Any:       {"z", "y", "f"},
}

// repOf is the key a pattern row is pressed with, "" when none fits.
func repOf(pattern string) string {
	if set, ok := strings.CutPrefix(pattern, "one-of:"); ok {
		if f := strings.Fields(set); len(f) > 0 {
			return f[0]
		}
		return ""
	}
	class, minus, _ := strings.Cut(pattern, "-")
	excluded := strings.Split(minus, "-")
	for _, k := range classReps[class] {
		if !slices.Contains(excluded, k) {
			return k
		}
	}
	return ""
}

// nextKey is the key a row is pressed with next: in a sequence prefix the
// key after the keys typed so far, else the row's first key.
func nextKey(row ochazuke.Binding, prefix string) string {
	if row.Keys == "" {
		return repOf(row.Pattern)
	}
	keys := strings.Fields(row.Keys)
	if prefix != "" {
		typed := strings.Fields(prefix)
		if len(keys) > len(typed) && slices.Equal(keys[:len(typed)], typed) {
			return keys[len(typed)]
		}
	}
	return keys[0]
}

// actionsOf lists the actions tried in a state, in a fixed order: the keys
// the effective rows answer to (sorted), the hostile keys not already
// among them, Wait, then a resize to each size.
func actionsOf(r *ochazuke.Runtime, sizes [][2]int) []string {
	seen := map[string]bool{}
	var keys []string
	prefix := r.Mode.Payload.Prefix
	for _, row := range r.ListBindings(r.Focus) {
		if row.Unbind {
			continue
		}
		if k := nextKey(row, prefix); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range Hostile {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	keys = append(keys, Wait)
	for _, size := range sizes {
		keys = append(keys, Resize(size[0], size[1]))
	}
	return keys
}
