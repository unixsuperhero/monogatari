package monogatari

import "testing"

// The current cell is the text underlined in the reverse bar, reverse
// with a background (ochazuke's list.cell.cursor, list.cell-cursor
// background), or underlined on a background on a row holding a reverse
// bar (where highlights cross, ochazuke draws it out of the bar); a
// reverse run with a foreground colour alone (a faded bar) is no cell;
// a colour's own
// numbers (38;2;4;...) are never read as attributes.
func TestCellText(t *testing.T) {
	for _, c := range []struct{ row, want string }{
		{"\x1b[7m a \x1b[m\x1b[4;7mcell\x1b[m\x1b[7m b\x1b[m", "cell"},
		{"\x1b[7m a \x1b[m\x1b[1;38;2;26;26;26;48;2;224;178;67;4mcross\x1b[m\x1b[7m b\x1b[m", "cross"},
		{"plain \x1b[48;2;1;2;3;4mfill\x1b[m", ""},
		{"\x1b[7m a \x1b[m\x1b[38;2;4;4;4mtint\x1b[m", ""},
		{"\x1b[7m a \x1b[m\x1b[7;1;38;2;120;191;165;48;2;23;39;32mcursor\x1b[m\x1b[7m b\x1b[m", "cursor"},
		{"\x1b[38;2;9;9;9m\x1b[7m faded bar \x1b[m", ""},
		{"no cell", ""},
	} {
		if got := cellText(c.row); got != c.want {
			t.Errorf("cellText(%q) = %q, want %q", c.row, got, c.want)
		}
	}
}
