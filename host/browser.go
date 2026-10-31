package host

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

func declareIndexSettings(r *ochazuke.Runtime) error {
	return r.DeclareSetting(ochazuke.Setting{Name: "index.preview", Type: ochazuke.SettingBoolean, Default: true, Description: "Split the story index with a cached first frame and script; off restores the single list and enter opens live"})
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

// Only one build runs at a time. On completion the loop requests the latest
// focus, skipping entries passed over during a slow build. Cache hits do no work.
func (h *host) previewCommand() tea.Cmd {
	if h.previewBusy || h.cur != nil || h.play != nil || h.idx.Runtime.Page != "index" || !h.idx.Runtime.Settings().Bool("index.preview") {
		return nil
	}
	i := h.focusedStory()
	if i < 0 {
		return nil
	}
	if _, ok := h.previews[i]; ok {
		return nil
	}
	h.previewBusy = true
	lib, st := h.lib, h.lib.Stories[i]
	return func() tea.Msg {
		p, err := newPlayer(lib, st)
		pv := storyPreview{err: err}
		if err == nil {
			pv.frame = p.d.S.Frame()
			p.Close()
			p.d = nil
			pv.player = p
		}
		return previewReady{index: i, preview: pv}
	}
}

func (h *host) renderIndex(r *ochazuke.Runtime, w, ht int) ochazuke.Block {
	if !r.Settings().Bool("index.preview") && !h.embedded {
		return ochazuke.Text(h.renderStoryList(r, w, ht, false))
	}
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	layout := r.PaneLayout("index", ochazuke.Layout{Width: w, Height: ht, Styles: styles})
	side := max(1, min(w-2, layout.Divide((w-1)*2/5, w-1, ochazuke.PaneMinWidth)))
	right := max(1, w-side-1)
	left := strings.Split(h.renderStoryList(r, side, ht, true), "\n")
	var body string
	if h.embedded && h.play != nil {
		r.Placed("index-player")
		body = h.renderPlay(r, right, ht).Text
	} else {
		body = h.renderPreview(right, ht)
	}
	return ochazuke.Text(beside(left, strings.Split(body, "\n"), side, w, ht))
}

// Each area's list keeps its own window; all lists are placed, including
// those outside the pane, so the framework retains cross-area motions.
func (h *host) renderStoryList(r *ochazuke.Runtime, w, ht int, window bool) string {
	styles := ochazuke.NewStyles(*h.idx.Appearance)
	var lines []string
	if h.msg != "" {
		lines = append(lines, styles.Subtitle.Render(ansi.Truncate(h.msg, w, "…")), "")
	}
	if window {
		focused := -1
		if i := h.focusedStory(); i >= 0 {
			for n, a := range h.areas {
				if a == h.lib.Stories[i].Area {
					focused = n
					break
				}
			}
		}
		if focused >= 0 {
			if focused < h.indexTop {
				h.indexTop = focused
			}
			needed := len(lines)
			for n := h.indexTop; n <= focused; n++ {
				needed += r.List("area-"+h.areas[n]).Height() + 3
			}
			if needed > ht {
				h.indexTop = focused
			}
		}
	}
	for n, a := range h.areas {
		l := r.List("area-" + a)
		height := l.Height() + 1
		if window {
			if n < h.indexTop || len(lines)+2 >= ht {
				l.View(max(1, w-areaIndent), 0)
				continue
			}
			height = min(height, max(1, ht-len(lines)-2))
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, areaHeading(styles, a, l.Len(), w))
		lines = append(lines, strings.Split(indent(l.View(max(1, w-areaIndent), height), areaIndent), "\n")...)
	}
	return strings.Join(lines, "\n")
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
	lines := []string{styles.Title.Render(ansi.Truncate(st.Name, w, "…")), fmt.Sprintf("area: %s · fixture: %s", st.Area, fixture), fmt.Sprintf("app: %s · %dx%d", app, sw, sh)}
	pv, ready := h.previews[i]
	if !ready {
		return strings.Join(append(lines, "", "Loading preview…"), "\n")
	}
	if pv.err != nil {
		return strings.Join(append(lines, "", "Preview: "+pv.err.Error()), "\n")
	}
	scriptHeight := max(2, (ht-len(lines)-2)/2)
	if len(pv.player.story.Actions) > 0 {
		lines = append(lines, pv.player.treeSidebar(styles, w, scriptHeight, false)...)
	} else {
		lines = append(lines, pv.player.sidebar(styles, w, scriptHeight)...)
	}
	lines = append(lines, "")
	lines = append(lines, boxFrame(pv.frame, " First frame ", fmt.Sprintf(" %dx%d ", sw, sh), sw)...)
	return strings.Join(lines, "\n")
}
