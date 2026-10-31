package host

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// inspection includes the app seam's existing write log. No app knowledge
// belongs in the player, and the story frame is never changed for the strip.
func (p *player) inspection() ochazuke.Inspection {
	if p.d == nil {
		return ochazuke.Inspection{}
	}
	state := p.d.S.R.Inspect()
	for _, path := range p.d.S.Writes() {
		if !slices.Contains(state.FilesWritten, path) {
			state.FilesWritten = append(state.FilesWritten, path)
		}
	}
	return state
}

// inspectorStrip puts changed fields first and marks them with both an
// accent and a delta, so changes remain visible with monochrome themes.
func (p *player) inspectorStrip(s ochazuke.Styles, width int) []string {
	previous := map[string]string{}
	for _, part := range p.before.Parts() {
		previous[part.Key] = part.Text
	}
	current := p.inspection().Parts()
	var changed, rest []string
	for _, part := range current {
		if previous[part.Key] != part.Text {
			changed = append(changed, "Δ "+part.Text)
		} else {
			rest = append(rest, part.Text)
		}
		delete(previous, part.Key)
	}
	// Removed values (closed modals, cleared registers) are changes too.
	var removed []string
	for _, part := range p.before.Parts() {
		if _, ok := previous[part.Key]; ok {
			removed = append(removed, fmt.Sprintf("Δ cleared %q", part.Key))
		}
	}
	changed = append(changed, removed...)
	var lines []string
	if len(changed) > 0 {
		for _, line := range strings.Split(ansi.Wrap(strings.Join(changed, " · "), width, ""), "\n") {
			lines = append(lines, s.Accent.Bold(true).Render(line))
		}
	}
	for _, line := range strings.Split(ansi.Wrap(strings.Join(rest, " · "), width, ""), "\n") {
		lines = append(lines, s.Muted.Render(line))
	}
	if len(lines) > 3 {
		lines = lines[:3]
		lines[2] = ansi.Truncate(lines[2], max(0, width-1), "") + "…"
	}
	return lines
}
