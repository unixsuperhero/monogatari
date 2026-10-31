package monogatari

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunPosition is a completed position in Run. Action -1 is setup or an
// ordinary script; After 0 is the opening, before any segment events.
type RunPosition struct {
	Session *Session
	Action  int
	After   int
}

func (o RunOptions) observe(d *Driver, action, after int) {
	if o.Observe != nil {
		o.Observe(RunPosition{d.S, action, after})
	}
}

// ExportedStory is a portable replay with no capture timestamps.
// Version changes when the export schema changes.
type ExportedStory struct {
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Area     string          `json:"area"`
	App      string          `json:"app"`
	Fixture  string          `json:"fixture"`
	Width    int             `json:"width"`
	Height   int             `json:"height"`
	Chapters []ExportChapter `json:"chapters"`
}

type ExportChapter struct {
	ID      string       `json:"id,omitempty"`
	Label   string       `json:"label"`
	Caption string       `json:"caption,omitempty"`
	Steps   []ExportStep `json:"steps"`
}

type ExportStep struct {
	Label   string   `json:"label"`
	Caption string   `json:"caption,omitempty"`
	Keys    []string `json:"keys"`
	Expect  Expects  `json:"expect"`
	HTML    string   `json:"html"`
}

// Export runs the same driver and assertions as Run, including golden
// checks for ordinary scripts and a fresh fixture and setup for each action.
// It returns no replay for a failing or skipped story.
func (lib Library) Export(s *Story) (ExportedStory, error) {
	// A stable path is required before rendering: clipping and layout can
	// depend on a path's length. Exclusive creation also prevents concurrent
	// exporters from sharing or deleting one another's fixture files.
	root := filepath.Join(os.TempDir(), "monogatari-export")
	if err := os.Mkdir(root, 0o700); err != nil {
		return ExportedStory{}, fmt.Errorf("export sandbox %s: %w", root, err)
	}
	defer os.RemoveAll(root)
	out := ExportedStory{Version: 1, ID: strings.TrimSuffix(filepath.Base(s.Path), ".json"), Name: s.Name, Area: s.Area}
	if out.ID == "" || out.ID == "." {
		out.ID = Slug(s.Name)
	}
	out.Width, out.Height = s.Size()
	label := "Script"
	if len(s.Actions) > 0 {
		label = "Setup"
	}
	out.Chapters = append(out.Chapters, ExportChapter{Label: label})
	for _, a := range s.Actions {
		out.Chapters = append(out.Chapters, ExportChapter{ID: a.ID, Label: a.Label, Caption: a.Caption})
	}
	res := lib.Run(s, RunOptions{Golden: true, exportRoot: root, Observe: func(p RunPosition) {
		ss := p.Session
		out.App, out.Fixture = ss.App.Name, ss.Fixture.Name
		out.Width, out.Height = ss.Story.Size()
		st := ss.Story
		events, notes, expects := st.Script, st.Notes, st.Expect
		ci := 0
		if len(st.Actions) > 0 {
			events = st.Setup
		}
		if p.Action >= 0 {
			a := st.Actions[p.Action]
			ci, events, notes, expects = p.Action+1, a.Keys, a.Notes, a.Expect
		}
		step := ExportStep{Label: "Opening", Keys: []string{}, Expect: Expects{}}
		if p.Action >= 0 {
			step.Label = "Reset point"
		}
		if p.After > 0 {
			e := events[p.After-1]
			step.Label, step.Caption = e.String(), e.Note
			if e.Key != "" {
				step.Keys = append(step.Keys, e.Key)
			}
			if e.Type != "" {
				step.Keys = append(step.Keys, "type "+e.Type)
			}
			if e.Paste != "" {
				step.Keys = append(step.Keys, "paste "+e.Paste)
			}
		}
		if len(st.Actions) > 0 && p.Action == -1 && p.After == len(events) {
			step.Label = "Reset point"
		}
		for _, n := range notes {
			if n.After == p.After {
				if step.Caption != "" {
					step.Caption += "\n"
				}
				step.Caption += n.Note
			}
		}
		if p.Action >= 0 && p.After == 0 {
			for _, e := range st.Expect {
				if e.After == 0 {
					step.Expect = append(step.Expect, e)
				}
			}
		}
		for _, e := range expects {
			if e.After == p.After && e.After > 0 || e.After == 0 && p.After == len(events) {
				step.Expect = append(step.Expect, e)
			}
		}
		// Frame is the player's exact rendering. Decode its ANSI with the same
		// ultraviolet cell model used by Bubble Tea, then serialize styled runs.
		step.HTML = FrameHTML(ss.Frame())
		out.Chapters[ci].Steps = append(out.Chapters[ci].Steps, step)
	}})
	if res.Skipped {
		return ExportedStory{}, fmt.Errorf("%s: skipped story: %s", s.Name, s.Skip)
	}
	if res.Err != nil {
		return ExportedStory{}, fmt.Errorf("%s: %w", s.Name, res.Err)
	}
	if len(res.Failed) > 0 {
		return ExportedStory{}, fmt.Errorf("%s: %s", s.Name, strings.Join(res.Failed, "; "))
	}
	return out, nil
}
