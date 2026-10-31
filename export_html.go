package monogatari

import (
	"fmt"
	"html"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// FrameHTML serializes the player's trusted ANSI frame as escaped HTML.
// Adapted from tuios/shot/html.go's row/run writer. Ultraviolet handles
// ANSI decoding, grapheme widths and styles; this is only an HTML backend.
// Adjacent cells of the same style share a span. Wide-cell tails are omitted.
func FrameHTML(frame string) string {
	var b strings.Builder
	for y, line := range uv.NewStyledString(frame).Lines(ansi.GraphemeWidth) {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < len(line); {
			c := line[x]
			x++
			if c.Width == 0 {
				continue
			}
			var text strings.Builder
			text.WriteString(c.Content)
			for x < len(line) {
				n := line[x]
				if n.Width == 0 {
					x++
					continue
				}
				if !c.Style.Equal(&n.Style) {
					break
				}
				text.WriteString(n.Content)
				x++
			}
			css := cellCSS(c.Style)
			if css != "" {
				b.WriteString(`<span style="` + css + `">`)
			}
			b.WriteString(html.EscapeString(text.String()))
			if css != "" {
				b.WriteString(`</span>`)
			}
		}
	}
	return b.String()
}

func cellCSS(s uv.Style) string {
	var css []string
	fg, bg := cssColor(s.Fg), cssColor(s.Bg)
	if s.Attrs&uv.AttrReverse != 0 {
		if fg == "" {
			fg = "var(--frame-fg,#cdd6f4)"
		}
		if bg == "" {
			bg = "var(--frame-bg,#1e1e2e)"
		}
		fg, bg = bg, fg
	}
	if fg != "" {
		css = append(css, "color:"+fg)
	}
	if bg != "" {
		css = append(css, "background:"+bg)
	}
	if s.Attrs&uv.AttrBold != 0 {
		css = append(css, "font-weight:bold")
	}
	if s.Attrs&uv.AttrFaint != 0 {
		css = append(css, "opacity:0.55")
	}
	if s.Attrs&uv.AttrItalic != 0 {
		css = append(css, "font-style:italic")
	}
	if s.Attrs&uv.AttrConceal != 0 {
		css = append(css, "visibility:hidden")
	}
	var deco []string
	if s.Attrs&uv.AttrStrikethrough != 0 {
		deco = append(deco, "line-through")
	}
	if s.Underline != uv.UnderlineNone {
		deco = append(deco, "underline")
	}
	if len(deco) > 0 {
		css = append(css, "text-decoration-line:"+strings.Join(deco, " "))
	}
	switch s.Underline {
	case uv.UnderlineDouble:
		css = append(css, "text-decoration-style:double")
	case uv.UnderlineCurly:
		css = append(css, "text-decoration-style:wavy")
	case uv.UnderlineDotted:
		css = append(css, "text-decoration-style:dotted")
	case uv.UnderlineDashed:
		css = append(css, "text-decoration-style:dashed")
	}
	if c := cssColor(s.UnderlineColor); c != "" {
		css = append(css, "text-decoration-color:"+c)
	}
	// Blink attributes intentionally stay static in exported frames.
	return strings.Join(css, ";")
}

func cssColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}
