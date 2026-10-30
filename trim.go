package monogatari

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// TrimSpec chooses a window of a recording's (or story's) events.
type TrimSpec struct {
	From, To int    // inclusive event indexes (0-based); -1 = unset
	At       int    // the failing event; From/To default to At-3 and At+2; -1 = unset
	As       string // the new story's name
	Snapshot bool   // bake the before-state into the story's app data (Built.Snapshot) instead of naming a base
}

// Window resolves From and To against n events. A window is never a
// single frame: it holds at least two events.
func (t TrimSpec) Window(n int) (int, int, error) {
	from, to := t.From, t.To
	if t.At >= 0 {
		if from < 0 {
			from = t.At - 3
		}
		if to < 0 {
			to = t.At + 2
		}
	}
	if from < 0 {
		from = 0
	}
	if to < 0 || to >= n {
		to = n - 1
	}
	if n < 2 {
		return 0, 0, fmt.Errorf("%d events: a window needs at least two", n)
	}
	if to <= from {
		if from >= n-1 {
			from = n - 2
		}
		to = from + 1
	}
	return from, to, nil
}

// Trim writes a story of events[from..to] of the source. Without
// Snapshot it names its base ("recording:NAME" or "story:NAME") and the
// runner replays events[:from] headless first; with Snapshot it plays them
// now and bakes the state they reach (lists, rows, selection, marks,
// focus, page offset) into the story's pages, so the story stands alone.
// The expect is the state observed at the end of the window: edit it to
// the correct state to make the story fail until the bug is fixed.
func (lib Library) Trim(base string, initial *Story, events []Event, t TrimSpec) (*Story, []string, error) {
	from, to, err := t.Window(len(events))
	if err != nil {
		return nil, nil, err
	}
	if t.As == "" {
		return nil, nil, errors.New("trim needs a name (--as)")
	}
	window := slices.Clone(events[from : to+1])
	for i := range window {
		window[i].T = 0
	}
	out := &Story{Name: t.As, Area: "trimmed", Script: window}
	var warn []string
	ss, err := lib.Build(initial, Options{Mono: true})
	if err != nil {
		return nil, nil, err
	}
	d := NewDriver(ss, Options{Mono: true})
	defer d.Close()
	if err := d.Play(events[:from], nil); err != nil {
		return nil, nil, fmt.Errorf("replaying to %d: %w", from, err)
	}
	if t.Snapshot {
		snap, err := d.S.Snapshot()
		if err != nil {
			return nil, nil, err
		}
		warn = snap.Warn
		out.Width, out.Height = initial.Size()
		out.Chrome = initial.Chrome
		out.App, out.Fixture, out.Seed = initial.App, initial.Fixture, initial.Seed
		if snap.Seed != nil {
			out.Seed = snap.Seed
		}
		out.Fields = snap.Fields
		// The window must play from the baked state alone.
		baked, err := lib.Build(out, Options{Mono: true})
		if err != nil {
			return nil, warn, err
		}
		d.Close()
		d = NewDriver(baked, Options{Mono: true})
		defer d.Close()
	} else {
		out.Base, out.Start = base, from
	}
	if err := d.Play(window, nil); err != nil {
		if t.Snapshot {
			return nil, warn, fmt.Errorf("the snapshot cannot carry this before-state (trim without --snapshot): %w", err)
		}
		return nil, warn, fmt.Errorf("window: %w", err)
	}
	out.Expect = Expects{{Focus: d.S.R.Focus, Mode: d.S.R.Mode.Name}}
	warn = append(warn, fmt.Sprintf("window is events %d..%d; expect is the observed state, edit it to the correct one", from, to))
	return out, warn, nil
}

// WriteStory writes a story as <dir>/<area>/<slug>.json.
func WriteStory(dir string, s *Story) (string, error) {
	path := filepath.Join(dir, s.Area, Slug(s.Name)+".json")
	return path, writeJSON(path, s)
}
