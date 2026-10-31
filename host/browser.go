package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
)

func declareIndexSettings(r *ochazuke.Runtime) error {
	// v0.54 keeps this default on Page. Leave a later framework declaration
	// in charge when it exposes the same setting itself.
	if !slices.ContainsFunc(r.Settings().Declared(), func(s ochazuke.Setting) bool { return s.Name == "scrolloff" }) {
		if err := r.DeclareSetting(ochazuke.Setting{Name: "scrolloff", Type: ochazuke.SettingInteger, Default: 2, Description: "Rows kept above and below the focus", Validate: func(v any) error {
			if v.(int) < 0 {
				return fmt.Errorf("must be nonnegative")
			}
			return nil
		}}); err != nil {
			return err
		}
	}
	for _, setting := range []ochazuke.Setting{
		{Name: "index.preview", Type: ochazuke.SettingBoolean, Default: true, Description: "Split the story index with a cached first frame and script; off restores the single list and enter opens live"},
		{Name: "index.playing-row", Type: ochazuke.SettingBoolean, Default: true, Description: "Keep the pane player's story row in view and mark it with an arrow"},
		{Name: "play.follow-focus", Type: ochazuke.SettingBoolean, Default: true, Description: "Cancel player startup when focus moves away from its story"},
		{Name: "story.build-timeout", Type: ochazuke.SettingDuration, Default: "5s", Description: "Maximum wait for a story fixture to start or preview", Validate: func(v any) error {
			if v.(time.Duration) <= 0 {
				return fmt.Errorf("must be positive")
			}
			return nil
		}},
		{Name: "play.start-indicator", Type: ochazuke.SettingBoolean, Default: true, Description: "Show Starting in the preview pane while a player is being built"},
	} {
		if err := r.DeclareSetting(setting); err != nil {
			return err
		}
	}
	return nil
}

// A preview owns only a frame and the player's unplayed script. The worker
// closes its session before delivering this immutable snapshot to the loop.
type storyPreview struct {
	player *player
	frame  string
	err    error
}
type previewReady struct {
	index   int
	gen     int
	preview storyPreview
}

func (h *host) focusedStory() int {
	id := h.idx.Runtime.Focus
	if h.embedded {
		id = h.returnFocus
	}
	inst := h.idx.Runtime.Instances[id]
	if inst != nil && inst.Noun == "entry" {
		if i, ok := inst.Data.(int); ok {
			return i
		}
	}
	return -1
}

// New focus replaces the preview slot immediately. Each command owns a
// cancellation context; stale commands skip the build even if not yet started.
func (h *host) previewCommand() tea.Cmd {
	i := h.focusedStory()
	eligible := h.cur == nil && h.play == nil && h.starting == nil && h.idx.Runtime.Page == "index" && h.idx.Runtime.Settings().Bool("index.preview") && i >= 0
	if eligible && h.previewBusy && h.previewIndex == i {
		return nil
	}
	if h.previewCancel != nil {
		h.previewCancel()
		h.previewCancel = nil
	}
	h.previewBusy = false
	if !eligible {
		return nil
	}
	if _, ok := h.previews[i]; ok {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.previewCancel, h.previewBusy, h.previewIndex = cancel, true, i
	h.previewGen++
	gen, lib, st, slot := h.previewGen, h.lib, h.lib.Stories[i], h.previewSlot
	wait := h.idx.Runtime.Settings().Duration("story.build-timeout")
	return func() tea.Msg {
		p, err := buildPlayerInSlot(ctx, lib, st, wait, slot)
		pv := storyPreview{err: err}
		if err == nil {
			pv.frame = p.d.S.Frame()
			p.Close()
			p.d = nil
			pv.player = p
		}
		return previewReady{index: i, gen: gen, preview: pv}
	}
}

func (h *host) renderIndex(r *ochazuke.Runtime, w, ht int) ochazuke.Block {
	if !r.Settings().Bool("index.preview") && !h.embedded {
		return ochazuke.Text(h.renderStoryList(r, w, ht, true))
	}
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	layout := r.PaneLayout("index", ochazuke.Layout{Width: w, Height: ht, Styles: styles})
	side := max(1, min(w-4, layout.Divide((w-3)*2/5, w-3, ochazuke.PaneMinWidth)))
	right := max(1, w-side-3)
	left := strings.Split(h.renderStoryList(r, side, ht, true), "\n")
	var body string
	if h.embedded && h.play != nil {
		r.Placed("index-player")
		body = h.renderPlay(r, right, ht).Text
	} else if h.starting != nil && r.Settings().Bool("play.start-indicator") {
		body = "Starting…\n" + ansi.Wrap(h.starting.Name, right, "")
	} else {
		body = h.renderPreview(right, ht)
	}
	rows := strings.Split(body, "\n")
	out := make([]string, ht)
	for i := range out {
		out[i] = layoutRow(left, i, side) + styles.Muted.Render(" │ ") + layoutRow(rows, i, right)
	}
	return ochazuke.Text(strings.Join(out, "\n"))
}

// Window the combined area surface by screen rows, preserving the lists'
// drawing order and reporting their placements after clipping. Using the same
// window in both layouts also keeps focus visible on the first opt-out frame.
func (h *host) renderStoryList(r *ochazuke.Runtime, w, ht int, window bool) string {
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	type section struct {
		id                string
		start, end, focus int
	}
	var sections []section
	var lines []string
	focused := -1
	for _, a := range h.areas {
		l := r.List("area-" + a)
		start := len(lines)
		if start > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, areaHeading(styles, a, l.Len(), w))
		focus := len(lines) + 1 + l.Snapshot()["selected"].(int)
		height := max(2, l.Shown()+1)
		if l.Filtering() {
			height++
			focus++
		}
		lines = append(lines, strings.Split(indent(l.View(max(1, w-areaIndent), height), areaIndent), "\n")...)
		sections = append(sections, section{"area-" + a, start, len(lines), focus})
		if l.Focused() {
			focused = focus
		}
		if h.embedded && h.play != nil && r.Settings().Bool("index.playing-row") && l.FocusTarget() == h.returnFocus {
			focused = focus
			if focus < len(lines) {
				lines[focus] = styles.Muted.Render("▶ ") + strings.TrimPrefix(lines[focus], strings.Repeat(" ", areaIndent))
			}
		}
	}
	top, bottom := 0, len(lines)
	if window {
		margin := min(max(0, r.Settings().Int("scrolloff")), max(0, (ht-1)/2))
		if focused >= 0 {
			if focused+margin >= h.indexTop+ht {
				h.indexTop = focused + margin - ht + 1
			}
			if focused-margin < h.indexTop {
				h.indexTop = focused - margin
			}
		}
		h.indexTop = min(max(0, h.indexTop), max(0, len(lines)-ht))
		top, bottom = h.indexTop, min(len(lines), h.indexTop+ht)
	}
	var out []string
	for _, part := range sections {
		start, end := max(top, part.start), min(bottom, part.end)
		if start >= end {
			continue
		}
		text := strings.Join(lines[start:end], "\n")
		out = append(out, r.PlacedAt(part.id, text, part.focus-start, 1))
	}
	return strings.Join(out, "\n")
}

func (h *host) renderPreview(w, ht int) string {
	i := h.focusedStory()
	if i < 0 {
		return "No story focused"
	}
	st := h.lib.Stories[i]
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	app, fixture := storyFixture(h.lib, st)
	sw, sh := st.Size()
	lines := []string{styles.Title.Render(ansi.Truncate(st.Name, w, "…"))}
	for _, text := range []string{fmt.Sprintf("area: %s · fixture: %s", st.Area, fixture), fmt.Sprintf("app: %s · %dx%d", app, sw, sh)} {
		lines = append(lines, strings.Split(ansi.Wrap(text, max(1, w), ""), "\n")...)
	}
	pv, ready := h.previews[i]
	// Script metadata needs no fixture. Even a broken fixture keeps its
	// known keys, assertions, and tutorial tree in the preview.
	p := pv.player
	if p == nil {
		resolved, _, err := h.lib.Resolve(st)
		if err != nil {
			resolved = st
		}
		p = &player{story: resolved, action: -1, steps: map[int][]story.Expect{}}
		for _, e := range resolved.Expect {
			p.steps[e.After] = append(p.steps[e.After], e)
		}
	}
	scriptHeight := max(4, (ht-len(lines)-2)/2)
	if len(p.story.Actions) > 0 {
		lines = append(lines, p.treeSidebar(styles, w, scriptHeight, false)...)
	} else {
		lines = append(lines, p.sidebar(styles, w, scriptHeight)...)
	}
	lines = append(lines, "")
	switch {
	case !ready:
		lines = append(lines, "Loading preview…")
	case pv.err != nil:
		text := "Preview unavailable: " + previewError(pv.err)
		for _, row := range strings.Split(ansi.Wrap(text, max(1, w), ""), "\n") {
			lines = append(lines, styles.Error.Render(row))
		}
	default:
		// Adapted from ochazuke surface.dim: reopen the dim tone after each
		// inner reset so a fixture's reverse cursor stays secondary.
		dim := styles.WithDims(0, h.idx.Runtime.Settings().Int("dim-unfocused")).Unfocused
		sample := dim.Render("x")
		open := sample[:strings.Index(sample, "x")]
		frame := boxFrame(pv.frame, " First frame ", fmt.Sprintf(" %dx%d ", sw, sh), sw)
		for _, row := range frame {
			if open != "" {
				row = strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+open)
				row = strings.ReplaceAll(row, "\x1b[m", "\x1b[m"+open)
				row = open + row + "\x1b[m"
			}
			lines = append(lines, row)
		}
	}
	return strings.Join(lines, "\n")
}

// JSON type errors otherwise print the fixture's Go struct definition,
// obscuring which value the owner needs to fix in the story seed.
func previewError(err error) string {
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		shapes := map[reflect.Kind]string{
			reflect.Slice: "a list", reflect.Array: "a list",
			reflect.Map: "an object", reflect.Struct: "an object",
			reflect.String: "text", reflect.Bool: "true or false",
		}
		shape := shapes[mismatch.Type.Kind()]
		if shape == "" {
			shape = "a " + mismatch.Type.Kind().String() + " value"
		}
		where := "seed"
		if mismatch.Field != "" {
			where += "." + mismatch.Field
		}
		return fmt.Sprintf("The fixture expected %s at %s, but received %s.", shape, where, mismatch.Value)
	}
	return err.Error()
}
