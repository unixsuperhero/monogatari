package monogatari

import "testing"

func TestHighlightsSGR(t *testing.T) {
	for _, c := range []struct {
		name, row    string
		full, dimmed bool
	}{
		{"plain", "text", false, false},
		{"reverse", "\x1b[7mtext\x1b[m", true, false},
		{"reverse faint", "\x1b[7;2mtext\x1b[m", false, true},
		{"faint reverse", "\x1b[2;7mtext\x1b[m", false, true},
		{"separate reverse faint", "\x1b[7m\x1b[2mtext", false, true},
		{"separate faint reverse", "\x1b[2m\x1b[7mtext", false, true},
		{"no text", "\x1b[7m\x1b[m", false, false},
		{"trailing reverse", "text\x1b[7m", false, false},
		{"reset", "\x1b[7;2;0mtext", false, false},
		{"empty reset", "\x1b[7;2m\x1b[mtext", false, false},
		{"unreverse", "\x1b[7;2;27mtext", false, false},
		{"unfaint", "\x1b[7;2;22mtext", true, false},
		{"faint after unfaint", "\x1b[7;22;2mtext", false, true},
		{"faint after text", "\x1b[7mfull\x1b[2mdimmed", true, true},
		{"unfaint after text", "\x1b[7;2mdimmed\x1b[22mfull", true, true},
		{"unreverse after text", "\x1b[7;2mdimmed\x1b[27mplain", false, true},
		{"reset before reverse", "\x1b[2m\x1b[0;7mtext", true, false},
		{"foreground truecolor", "\x1b[38;2;7;2;0mtext", false, false},
		{"background truecolor", "\x1b[48;2;7;2;0mtext", false, false},
		{"reverse foreground truecolor", "\x1b[7;38;2;0;2;27mtext", true, false},
		{"reverse background truecolor", "\x1b[7;48;2;0;2;27mtext", true, false},
		{"truecolor before reverse faint", "\x1b[38;2;22;27;0;7;2mtext", false, true},
		{"truecolor after reverse faint", "\x1b[7;2;48;2;22;27;0mtext", false, true},
		{"foreground indexed", "\x1b[38;5;7mtext", false, false},
		{"background indexed", "\x1b[48;5;7mtext", false, false},
		{"reverse indexed", "\x1b[7;38;5;2mtext", true, false},
		{"dimmed indexed", "\x1b[2;7;48;5;22mtext", false, true},
		{"colour resets preserve selection", "\x1b[7;2;38;2;1;2;3;48;5;7;39;49mtext", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if full, dimmed := highlights(c.row); full != c.full || dimmed != c.dimmed {
				t.Fatalf("highlights(%q) = (%v, %v), want (%v, %v)", c.row, full, dimmed, c.full, c.dimmed)
			}
		})
	}
}

func TestGoldenReverseFaintOrder(t *testing.T) {
	for _, params := range []string{"7;2", "2;7"} {
		if got := Golden("\x1b[" + params + "mselected\x1b[m"); got != "r|selected\n" {
			t.Errorf("Golden with %s = %q, want dimmed selection", params, got)
		}
	}
}

func TestAttrsAtSGRResets(t *testing.T) {
	const styled = "\x1b[1;2;4;7;38;2;22;27;0;48;5;7m"
	for _, c := range []struct {
		name, reset string
		cleared     []string
	}{
		{"none", "", nil},
		{"all", "0", []string{"bold", "faint", "underline", "reverse", "fg", "bg"}},
		{"empty", "", []string{"bold", "faint", "underline", "reverse", "fg", "bg"}},
		{"intensity", "22", []string{"bold", "faint"}},
		{"underline", "24", []string{"underline"}},
		{"reverse", "27", []string{"reverse"}},
		{"foreground", "39", []string{"fg"}},
		{"background", "49", []string{"bg"}},
		{"colours", "39;49", []string{"fg", "bg"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			row := styled
			if c.name != "none" {
				row += "\x1b[" + c.reset + "m"
			}
			attrs, found := attrsAt(row+"text", "text")
			if !found {
				t.Fatal("text not found")
			}
			want := map[string]bool{"bold": true, "faint": true, "underline": true, "reverse": true, "fg": true, "bg": true}
			for _, attr := range c.cleared {
				want[attr] = false
			}
			for attr, active := range want {
				if attrs[attr] != active {
					t.Errorf("attribute %s = %v, want %v", attr, attrs[attr], active)
				}
			}
		})
	}
}
