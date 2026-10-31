package explore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"ofam.dev/ochazuke"
)

// State is what one moment of an application is keyed by: two moments are
// the same state only when every key would behave the same in both (see
// the package doc). Each field is a short text, so a state prints as it
// is compared.
type State struct {
	Page      string   `json:"page"`
	Focus     []string `json:"focus"`            // the focus path outward: "id:noun", a list row "row:noun@list"
	Mode      string   `json:"mode"`             // name, then "+count" "+op" "+shadow" … for the payload's parts
	Modals    []string `json:"modals,omitempty"` // "noun^return-noun/return-mode" outermost first
	Guards    []string `json:"guards"`           // every guard as "name" (holds) or "!name"
	Rows      string   `json:"rows"`             // hash of the rows that answer at the focus in the mode
	List      string   `json:"list,omitempty"`
	Input     string   `json:"input,omitempty"`
	Registers string   `json:"registers,omitempty"`
	Screen    string   `json:"screen,omitempty"` // Full: hash of the screen at 80x24
	End       string   `json:"end,omitempty"`    // "quit" or "panic": no keys follow
}

// Key is the state as one comparable string.
func (s State) Key() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ID is a short hash of the key, for reports.
func (s State) ID() string { return shortHash(s.Key()) }

// Home reports whether the state is home for the trap check: the initial
// page, normal mode with an empty payload, no modal open.
func (s State) Home(page string) bool {
	return s.End == "" && s.Page == page && s.Mode == "normal" && len(s.Modals) == 0
}

// String is the state as one line.
func (s State) String() string {
	if s.End != "" {
		return s.End
	}
	parts := []string{"page=" + s.Page, "mode=" + s.Mode, "focus=" + strings.Join(s.Focus, "<")}
	if len(s.Modals) > 0 {
		parts = append(parts, "modals="+strings.Join(s.Modals, ","))
	}
	if s.List != "" {
		parts = append(parts, "list="+s.List)
	}
	if s.Input != "" {
		parts = append(parts, "input="+s.Input)
	}
	if s.Registers != "" {
		parts = append(parts, "registers="+s.Registers)
	}
	parts = append(parts, "rows="+s.Rows)
	if s.Screen != "" {
		parts = append(parts, "screen="+s.Screen)
	}
	return strings.Join(parts, " ")
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// stateOf reads the state key of a session (never Screen; see fullKey).
func stateOf(ss *session) State {
	if ss.panic != nil {
		return State{End: "panic"}
	}
	if ss.quit {
		return State{End: "quit"}
	}
	r := ss.m.Runtime
	st := State{Page: r.Page, Mode: modeKey(r.Mode)}
	for _, id := range r.Ancestry(r.Focus) {
		inst := r.Instances[id]
		if row, list := rowOf(r, id); row {
			st.Focus = append(st.Focus, "row:"+inst.Noun+"@"+list)
			continue
		}
		st.Focus = append(st.Focus, id+":"+inst.Noun)
	}
	for _, inst := range r.Modals() {
		if inst != nil {
			// What closing it restores is part of the state: a palette
			// over a stuck mode is not the palette over normal.
			focus, mode := r.ModalReturn(inst.ID)
			back := ""
			if ret := r.Instances[focus]; ret != nil {
				back = ret.Noun
			}
			st.Modals = append(st.Modals, inst.Noun+"^"+back+"/"+modeKey(mode))
		}
	}
	for _, name := range r.GuardNames() {
		if guardHolds(r, name) {
			st.Guards = append(st.Guards, name)
		} else {
			st.Guards = append(st.Guards, "!"+name)
		}
	}
	st.Rows = rowsHash(r.ListBindings(r.Focus))
	st.List = listClass(r)
	if in := r.Input(r.Focus); in != nil {
		st.Input = inputClass(in)
	}
	if regs := r.Registers(); regs != nil {
		if regs.Macro.Recording != "" {
			st.Registers = "recording"
		}
		if regs.Active != "" {
			st.Registers += "+active"
		}
	}
	return st
}

// guardHolds evaluates a guard at the focus; one that panics is false.
func guardHolds(r *ochazuke.Runtime, name string) (holds bool) {
	defer func() {
		if recover() != nil {
			holds = false
		}
	}()
	holds, _ = r.GuardHolds(name, r.Focus)
	return holds
}

func modeKey(m ochazuke.ModeState) string {
	out := m.Name
	p := m.Payload
	if p.Count > 0 {
		out += "+count"
	}
	if p.OpCount > 0 {
		out += "+opcount"
	}
	if p.Op != "" {
		out += "+op:" + p.Op
	}
	if p.Shadow != nil {
		out += "+shadow:" + p.Shadow.Name
	}
	if p.Request != nil {
		out += "+request"
	}
	if p.Return != "" && p.Return != "normal" {
		out += "+return:" + p.Return
	}
	return out
}

// rowOf reports whether id is a list row and its list.
func rowOf(r *ochazuke.Runtime, id string) (bool, string) {
	inst := r.Instances[id]
	if inst == nil || r.List(id) != nil || inst.Parent == "" || r.List(inst.Parent) == nil {
		return false, ""
	}
	for _, item := range r.List(inst.Parent).Items() {
		if item.ID == id {
			return true, inst.Parent
		}
	}
	return false, ""
}

// focusedList is the list the focus is on (the list or one of its rows).
func focusedList(r *ochazuke.Runtime) (*ochazuke.List, string) {
	if l := r.List(r.Focus); l != nil {
		return l, r.Focus
	}
	if row, list := rowOf(r, r.Focus); row {
		return r.List(list), list
	}
	return nil, ""
}

func listClass(r *ochazuke.Runtime) string {
	l, _ := focusedList(r)
	if l == nil {
		return ""
	}
	snap := l.Snapshot()
	items, _ := snap["items"].([]ochazuke.ListItem)
	selected, _ := snap["selected"].(int)
	var pos string
	switch n := len(items); {
	case n == 0:
		pos = "empty"
	case n == 1:
		pos = "only"
	case selected <= 0:
		pos = "first"
	case selected >= n-1:
		pos = "last"
	default:
		pos = "middle"
	}
	if l.Filtering() {
		pos += "+filter"
	}
	if len(l.Marked()) > 0 {
		pos += "+marks"
	}
	if v, _ := snap["visual"].(bool); v {
		pos += "+visual"
	}
	return pos
}

func inputClass(in *ochazuke.Input) string {
	switch n := len(in.Text); {
	case n == 0:
		return "empty"
	case in.Cursor <= 0:
		return "start"
	case in.Cursor >= n:
		return "end"
	}
	return "middle"
}

// rowsHash hashes the effective rows: what each key runs, with what, under
// which guard.
func rowsHash(rows []ochazuke.Binding) string {
	var b strings.Builder
	for _, row := range rows {
		args, _ := json.Marshal(row.Request.Args)
		from, _ := json.Marshal(row.From)
		target := row.Request.Target
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%t|%t\n", row.Keys, row.Pattern, row.Request.Key(), args, from, target, row.When, row.Capture, row.Hidden)
	}
	return shortHash(b.String())
}
