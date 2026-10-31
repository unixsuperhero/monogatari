package monogatari

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Golden is a frame as a golden file holds it: each row stripped of
// escapes behind a gutter that reads "R|" when the row has full-strength
// reverse video, "r|" when its only reverse video is dimmed (faint: an
// unfocused list's selected row, a highlight beneath a modal) and " |"
// otherwise, so goldens pin the highlight without colour. A highlighted
// row holding a table's current cell (reverse and underline together)
// reads "C|", dimmed "c|".
func Golden(frame string) string {
	var b strings.Builder
	for _, row := range strings.Split(frame, "\n") {
		full, dimmed := highlights(row)
		cell := cellText(row) != ""
		switch {
		case full && cell:
			b.WriteString("C|")
		case full:
			b.WriteString("R|")
		case dimmed && cell:
			b.WriteString("c|")
		case dimmed:
			b.WriteString("r|")
		default:
			b.WriteString(" |")
		}
		b.WriteString(strings.TrimRight(ansi.Strip(row), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// highlights reports whether a row holds reverse video at full strength
// and whether it holds reverse video under the faint attribute (dimmed), by
// following the SGR state along the row.
func highlights(row string) (full, dimmed bool) {
	faint := false
	for i := 0; i < len(row); i++ {
		if row[i] != 0x1b || i+1 >= len(row) || row[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(row) && (row[j] >= '0' && row[j] <= '9' || row[j] == ';' || row[j] == ':') {
			j++
		}
		if j >= len(row) || row[j] != 'm' {
			continue
		}
		params := strings.Split(row[i+2:j], ";")
		for _, p := range params {
			switch p {
			case "", "0":
				faint = false
			case "2":
				faint = true
			case "22":
				faint = false
			case "7":
				if faint {
					dimmed = true
				} else {
					full = true
				}
			}
		}
		i = j
	}
	return full, dimmed
}

// cellText is the text of a row's current cell, trimmed: the text drawn
// underlined in reverse (a table's current cell inside its highlighted
// row) or underlined on a background colour (the current cell where
// highlights cross, ochazuke's intersect tone, drawn out of the reverse
// bar on a row that holds one), followed along the row's SGR state; " "
// for a blank cell and "" when the row holds none.
func cellText(row string) string {
	var b strings.Builder
	found, reverse, underline, bg := false, false, false, false
	bar := strings.Contains(row, "\x1b[7m") || strings.Contains(row, ";7m") || strings.Contains(row, "[7;")
	for i := 0; i < len(row); i++ {
		if row[i] == 0x1b && i+1 < len(row) && row[i+1] == '[' {
			j := i + 2
			for j < len(row) && (row[j] >= '0' && row[j] <= '9' || row[j] == ';' || row[j] == ':') {
				j++
			}
			if j < len(row) && row[j] == 'm' {
				params := strings.Split(row[i+2:j], ";")
				for k := 0; k < len(params); k++ {
					switch p := params[k]; p {
					case "", "0":
						reverse, underline, bg = false, false, false
					case "4":
						underline = true
					case "24":
						underline = false
					case "7":
						reverse = true
					case "27":
						reverse = false
					case "38", "48":
						// A colour's own numbers are not attributes.
						if p == "48" {
							bg = true
						}
						if k+1 < len(params) && params[k+1] == "5" {
							k += 2
						} else if k+1 < len(params) && params[k+1] == "2" {
							k += 4
						}
					case "49":
						bg = false
					}
				}
			}
			i = j
			continue
		}
		if underline && (reverse || bg && bar) {
			found = true
			b.WriteByte(row[i])
		}
	}
	if !found {
		return ""
	}
	if text := strings.TrimSpace(b.String()); text != "" {
		return text
	}
	return " "
}

// badgeShows reports whether the title row (the screen's first row) ends
// with the badge text want, padded by a cell on each side, or holds it
// right before the focus display's separator.
func badgeShows(screen, want string) bool {
	title, _, _ := strings.Cut(screen, "\n")
	title = strings.TrimRight(title, " ")
	if strings.HasSuffix(title, " "+want) {
		return true
	}
	for rest := title; ; {
		i := strings.Index(rest, " "+want+" ")
		if i < 0 {
			return false
		}
		rest = rest[i+len(want)+2:]
		if strings.HasPrefix(rest, "· ") {
			return true
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Check reports what in e does not hold for the session's current frame.
func Check(ss *Session, e Expect) []string {
	frame := ss.Frame()
	screen := ansi.Strip(frame)
	r := ss.R
	var bad []string
	failf := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }
	if e.Focus != "" && r.Focus != e.Focus {
		failf("focus %q, want %q", r.Focus, e.Focus)
	}
	if e.Mode != "" && r.Mode.Name != e.Mode {
		failf("mode %q, want %q", r.Mode.Name, e.Mode)
	}
	if e.Quit != nil && r.Quit != *e.Quit {
		failf("quit %v, want %v", r.Quit, *e.Quit)
	}
	if e.Modal != nil && r.TopModal() != *e.Modal {
		failf("top modal %q, want %q", r.TopModal(), *e.Modal)
	}
	if e.Page != "" && r.Page != e.Page {
		failf("page %q, want %q", r.Page, e.Page)
	}
	if e.Shift != nil && r.PaneShift(r.Page) != *e.Shift {
		failf("pane shift %d, want %d", r.PaneShift(r.Page), *e.Shift)
	}
	if e.Visible != nil {
		lbl := label(r.Instances[r.Focus])
		if on := strings.Contains(screen, lbl); on != *e.Visible {
			failf("focused %q on screen = %v, want %v", lbl, on, *e.Visible)
		}
	}
	if e.Highlighted != nil || e.Dimmed != nil {
		full, dimmed := 0, 0
		for _, row := range strings.Split(frame, "\n") {
			f, d := highlights(row)
			full += btoi(f)
			dimmed += btoi(d)
		}
		if e.Highlighted != nil && full != *e.Highlighted {
			failf("%d highlighted rows, want %d", full, *e.Highlighted)
		}
		if e.Dimmed != nil && dimmed != *e.Dimmed {
			failf("%d dimmed highlighted rows, want %d", dimmed, *e.Dimmed)
		}
	}
	if e.HighlightedText != "" {
		n := 0
		for _, row := range strings.Split(frame, "\n") {
			if full, _ := highlights(row); full {
				n++
				if !strings.Contains(ansi.Strip(row), e.HighlightedText) {
					failf("highlighted row %q lacks %q", strings.TrimSpace(ansi.Strip(row)), e.HighlightedText)
				}
			}
		}
		if n == 0 {
			failf("no highlighted row, want one holding %q", e.HighlightedText)
		}
	}
	if e.Cell != nil {
		got := ""
		for _, row := range strings.Split(frame, "\n") {
			if text := cellText(row); text != "" {
				got = text
				break
			}
		}
		if got != *e.Cell {
			failf("current cell %q, want %q", got, *e.Cell)
		}
	}
	if e.Badge != "" && !badgeShows(screen, e.Badge) {
		title, _, _ := strings.Cut(screen, "\n")
		failf("badge %q not on the title row %q", e.Badge, strings.TrimSpace(title))
	}
	for _, s := range e.Contains {
		if !strings.Contains(screen, s) {
			failf("screen lacks %q", s)
		}
	}
	for _, s := range e.Lacks {
		if strings.Contains(screen, s) {
			failf("screen shows %q", s)
		}
	}
	at := 0
	for _, s := range e.Order {
		i := strings.Index(screen[at:], s)
		if i < 0 {
			failf("order: %q not after offset %d", s, at)
			break
		}
		at += i + len(s)
	}
	w, _ := ss.Story.Size()
	ticker := ss.M.JobsTicker(w)
	if (e.Ticker != nil || e.NoTicker) && !slices.Equal(ticker, e.Ticker) {
		failf("ticker %q, want %q", ticker, e.Ticker)
	}
	if segs := ss.M.StatusSegments(); (e.Segments != nil || e.NoSegments) && !slices.Equal(segs, e.Segments) {
		failf("segments %q, want %q", segs, e.Segments)
	}
	if (e.Notices != nil || e.NoNotices) && !slices.Equal(ss.M.Notices, e.Notices) {
		failf("notices %q, want %q", ss.M.Notices, e.Notices)
	}
	rows := strings.Split(frame, "\n")
	for text, hex := range e.Colored {
		sgr, err := foreground(hex)
		if err != nil {
			failf("colored %q: %v", text, err)
			continue
		}
		found, drawn := false, false
		for _, row := range rows {
			if strings.Contains(ansi.Strip(row), text) {
				found = true
				drawn = drawn || strings.Contains(row, sgr)
			}
		}
		switch {
		case !found:
			failf("colored: screen lacks %q", text)
		case !drawn:
			failf("colored: no row with %q is drawn in %s", text, hex)
		}
	}
	for text, want := range e.Styled {
		hex, negate := strings.CutPrefix(want, "!")
		sgr, err := foreground(hex)
		if err != nil {
			failf("styled %q: %v", text, err)
			continue
		}
		run := regexp.MustCompile(`\x1b\[([0-9;:]*)m` + regexp.QuoteMeta(text))
		found, drawn := false, false
		for _, m := range run.FindAllStringSubmatch(frame, -1) {
			found = true
			drawn = drawn || strings.Contains(";"+m[1]+";", ";"+sgr+";")
		}
		switch {
		case !found:
			failf("styled: no styled run starts with %q", text)
		case drawn == negate:
			failf("styled: %q drawn in %s = %v, want %v", text, hex, drawn, !negate)
		}
	}
	for text, want := range e.Attrs {
		found := false
		for _, row := range rows {
			got, ok := attrsAt(row, text)
			if !ok {
				continue
			}
			found = true
			for _, a := range strings.Split(want, ",") {
				name, absent := strings.CutPrefix(strings.TrimSpace(a), "-")
				on, known := got[name]
				if !known {
					failf("attrs %q: unknown attribute %q (bold, faint, reverse, underline, fg, bg)", text, name)
					continue
				}
				if on == absent {
					failf("attrs: %q drawn %s = %v, want %v", text, name, on, !absent)
				}
			}
			break
		}
		if !found {
			failf("attrs: screen lacks %q", text)
		}
	}
	for _, hex := range e.NoColor {
		if sgr, err := foreground(hex); err != nil {
			failf("noColor %q: %v", hex, err)
		} else if strings.Contains(frame, sgr) {
			failf("noColor: the frame draws in %s", hex)
		}
	}
	if writes := ss.Writes(); e.Writes != nil && !slices.Equal(writes, *e.Writes) && (len(writes) > 0 || len(*e.Writes) > 0) {
		failf("writes %q, want %q", writes, *e.Writes)
	}
	if e.Register != nil {
		y, err := ss.R.ReadYank("\"")
		if err != nil {
			failf("register: %v", err)
		} else if y.Text != *e.Register {
			failf("register %q, want %q", y.Text, *e.Register)
		}
	}
	if len(bad) > 0 {
		bad = append(bad, "frame:\n"+Golden(frame))
	}
	return bad
}

// attrsAt is the attributes in force at the first cell of text in row
// (the first row holding it), following the row's SGR sequences.
func attrsAt(row, text string) (map[string]bool, bool) {
	at := strings.Index(ansi.Strip(row), text)
	if at < 0 {
		return nil, false
	}
	state := map[string]bool{"bold": false, "faint": false, "reverse": false, "underline": false, "fg": false, "bg": false}
	plain := 0 // bytes of stripped text passed
	for i := 0; i < len(row); {
		if row[i] == 0x1b && i+1 < len(row) && row[i+1] == '[' {
			j := i + 2
			for j < len(row) && (row[j] >= '0' && row[j] <= '9' || row[j] == ';' || row[j] == ':') {
				j++
			}
			if j < len(row) && row[j] == 'm' {
				sgr(state, strings.Split(row[i+2:j], ";"))
				i = j + 1
				continue
			}
		}
		if plain == at {
			return state, true
		}
		plain++
		i++
	}
	return state, true
}

// sgr applies SGR parameters to an attribute state; a colour's own
// parameters (38;2;R;G;B, 38;5;N) are skipped, not read as attributes.
func sgr(state map[string]bool, params []string) {
	for k := 0; k < len(params); k++ {
		switch p := params[k]; p {
		case "", "0":
			for a := range state {
				state[a] = false
			}
		case "1":
			state["bold"] = true
		case "2":
			state["faint"] = true
		case "22":
			state["bold"], state["faint"] = false, false
		case "4":
			state["underline"] = true
		case "24":
			state["underline"] = false
		case "7":
			state["reverse"] = true
		case "27":
			state["reverse"] = false
		case "39":
			state["fg"] = false
		case "38":
			state["fg"] = true
			if k+1 < len(params) && params[k+1] == "2" {
				k += 4
			} else if k+1 < len(params) && params[k+1] == "5" {
				k += 2
			}
		case "49":
			state["bg"] = false
		case "48", "58":
			if p == "48" {
				state["bg"] = true
			}
			if k+1 < len(params) && params[k+1] == "2" {
				k += 4
			} else if k+1 < len(params) && params[k+1] == "5" {
				k += 2
			}
		default:
			if n := len(p); n == 2 && (p[0] == '3' || p[0] == '9') && p[1] >= '0' && p[1] <= '7' {
				state["fg"] = true
			}
			if n := len(p); (n == 2 && p[0] == '4' || n == 3 && p[:2] == "10") && p[n-1] >= '0' && p[n-1] <= '7' {
				state["bg"] = true
			}
		}
	}
}

// foreground is the SGR parameters of a "#RRGGBB" foreground as Lip Gloss
// writes it ("38;2;R;G;B").
func foreground(hex string) (string, error) {
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil || len(hex) != 7 {
		return "", fmt.Errorf("want #RRGGBB, got %q", hex)
	}
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, b), nil
}

// Library is every story a run can name (embedded and the user's), the
// recordings store a trimmed story's base may name, and the apps whose
// fixtures build the stories (the first is the default; there is no
// built-in app).
type Library struct {
	Stories []*Story
	Store   Store
	Apps    []App
}

// Resolve turns a trimmed story into the base's initial story with this
// story's script and expectations, plus the base events to replay first.
// A base is "story:NAME" (its script) or "recording:NAME" (a saved
// recording's events).
func (lib Library) Resolve(s *Story) (*Story, []Event, error) {
	if s.Base == "" {
		return s, nil, nil
	}
	kind, name, _ := strings.Cut(s.Base, ":")
	var initial *Story
	var events []Event
	switch kind {
	case "story":
		base := Find(lib.Stories, name)
		if base == nil {
			return nil, nil, fmt.Errorf("base story %q not found", name)
		}
		b, prefix, err := lib.Resolve(base)
		if err != nil {
			return nil, nil, err
		}
		initial, events = b, append(slices.Clone(prefix), base.Script...)
	case "recording":
		e, err := lib.Store.Load(name)
		if err != nil {
			return nil, nil, err
		}
		initial, events = e.Rec.Initial, e.Rec.Events
	default:
		return nil, nil, fmt.Errorf("base %q: want story:NAME or recording:NAME", s.Base)
	}
	if s.Start > len(events) {
		return nil, nil, fmt.Errorf("start %d past the base's %d events", s.Start, len(events))
	}
	merged := *initial
	merged.Name, merged.Area, merged.Skip = s.Name, s.Area, s.Skip
	merged.Base, merged.Start = "", 0
	merged.Script, merged.Expect = s.Script, s.Expect
	merged.Path, merged.fsys, merged.rel, merged.disk = s.Path, s.fsys, s.rel, s.disk
	merged.Fields = maps.Clone(initial.Fields)
	return &merged, events[:s.Start], nil
}

// Result is one story's outcome.
type Result struct {
	Story   *Story
	Failed  []string
	Err     error
	Skipped bool   // the story carries a skip ("gap: ..."): it ran but cannot fail
	Updated bool   // --update wrote the golden
	Trace   []Step // the window's steps (the base's prefix excluded)
}

// OK reports a pass (or a skip).
func (r Result) OK() bool { return r.Skipped || (r.Err == nil && len(r.Failed) == 0) }

// RunOptions control one run.
type RunOptions struct {
	Update bool // rewrite goldens
	Golden bool // compare the final frame to a golden when the story has no final expect
	// Observe sees the opening and each completed event, using the same driver
	// and assertions as Run. The session is valid only during the callback.
	Observe    func(RunPosition)
	exportRoot string
}

// Run plays a story headless and checks it: each per-step expect after its
// step, the final expect at the end, else the golden frame.
func (lib Library) Run(s *Story, opt RunOptions) (res Result) {
	res = Result{Story: s, Skipped: s.Skip != ""}
	story, prefix, err := lib.Resolve(s)
	if err != nil {
		res.Err = err
		return res
	}
	if len(story.Actions) > 0 {
		return lib.runActions(story, opt)
	}
	ss, err := lib.Build(story, Options{Mono: true, TempRoot: opt.exportRoot})
	if err != nil {
		res.Err = err
		return res
	}
	d := NewDriver(ss, Options{Mono: true, TempRoot: opt.exportRoot})
	defer d.Close()
	if err := d.Play(prefix, nil); err != nil {
		res.Err = fmt.Errorf("base: %w", err)
		return res
	}
	d.Trace = nil
	opt.observe(d, -1, 0)
	defer func() { res.Trace = d.Trace }()
	var final []Expect
	steps := map[int][]Expect{}
	for _, e := range story.Expect {
		if e.After == 0 {
			final = append(final, e)
		} else {
			steps[e.After] = append(steps[e.After], e)
		}
	}
	err = d.Play(story.Script, func(n int) error {
		opt.observe(d, -1, n)
		for _, e := range steps[n] {
			for _, f := range Check(d.S, e) {
				res.Failed = append(res.Failed, fmt.Sprintf("after %d: %s", n, f))
			}
		}
		return nil
	})
	if err != nil {
		res.Err = err
		return res
	}
	for _, e := range final {
		res.Failed = append(res.Failed, Check(d.S, e)...)
	}
	if len(final) > 0 || !opt.Golden {
		return res
	}
	got := Golden(d.S.Frame())
	if opt.Update {
		res.Err = story.WriteGolden(got)
		res.Updated = res.Err == nil
		return res
	}
	want, err := story.ReadGolden()
	if errors.Is(err, fs.ErrNotExist) {
		res.Err = fmt.Errorf("no golden %s (run with --update)", story.GoldenPath())
		return res
	}
	if err != nil {
		res.Err = err
		return res
	}
	if string(want) != got {
		res.Failed = append(res.Failed, "frame differs from "+story.GoldenPath()+"\ngot:\n"+got+"want:\n"+string(want))
	}
	return res
}

// runActions checks each lesson in a fresh fixture, including fresh state
// and config directories. Optional light expects never require goldens.
func (lib Library) runActions(s *Story, opt RunOptions) Result {
	res := Result{Story: s, Skipped: s.Skip != ""}
	for i, a := range s.Actions {
		label := fmt.Sprintf("action %d (%s)", i+1, a.Label)
		ss, err := lib.Build(s, Options{Mono: true, TempRoot: opt.exportRoot})
		if err != nil {
			res.Err = errors.Join(res.Err, fmt.Errorf("%s: %w", label, err))
			continue
		}
		d := NewDriver(ss, Options{Mono: true, TempRoot: opt.exportRoot})
		check := func(ex Expects, after int) {
			for _, e := range ex {
				if e.After == after {
					for _, f := range Check(d.S, e) {
						res.Failed = append(res.Failed, label+": "+f)
					}
				}
			}
		}
		if i == 0 {
			opt.observe(d, -1, 0)
		}
		err = d.Play(s.Setup, func(n int) error {
			check(s.Expect, n)
			if i == 0 {
				opt.observe(d, -1, n)
			}
			return nil
		})
		if err == nil {
			check(s.Expect, 0)
			opt.observe(d, i, 0)
			err = d.Play(a.Keys, func(n int) error {
				check(a.Expect, n)
				opt.observe(d, i, n)
				return nil
			})
			if err == nil {
				check(a.Expect, 0)
			}
		}
		res.Trace = append(res.Trace, d.Trace...)
		d.Close()
		if err != nil {
			res.Err = errors.Join(res.Err, fmt.Errorf("%s: %w", label, err))
		}
	}
	return res
}

// Replay plays a recording headless: its events are the script, and only
// expect (the recording's own when nil) is asserted.
func (lib Library) Replay(rec *Recording, expect Expects) Result {
	s := *rec.Initial
	s.Base, s.Start, s.Script = "", 0, rec.Events
	s.Expect = rec.Expect
	if expect != nil {
		s.Expect = expect
	}
	return Library{Apps: lib.Apps}.Run(&s, RunOptions{})
}
