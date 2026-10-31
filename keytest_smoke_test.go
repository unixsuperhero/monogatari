package monogatari_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke/keytest"
)

// recorder is the smallest model keytest can drive: it keeps every message's
// key name, the names stories use.
type recorder struct{ got []string }

func (r *recorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		r.got = append(r.got, msg.String())
	case tea.PasteMsg:
		r.got = append(r.got, "paste:"+msg.Content)
	}
	return nil, nil
}

// TestKeytestSmoke proves monogatari can import ochazuke's exported keytest
// and that its key names round-trip through Bubble Tea's own key names.
func TestKeytestSmoke(t *testing.T) {
	r := &recorder{}
	for _, name := range []string{"ctrl+c", "esc", "enter", "shift+tab", "alt+b", "space", "x"} {
		keytest.Press(r, name)
	}
	keytest.Type(r, "ab")
	keytest.Paste(r, "yz")

	want := []string{"ctrl+c", "esc", "enter", "shift+tab", "alt+b", "space", "x", "a", "b", "paste:yz"}
	if len(r.got) != len(want) {
		t.Fatalf("got %d messages %q, want %q", len(r.got), r.got, want)
	}
	for i := range want {
		if r.got[i] != want[i] {
			t.Errorf("message %d = %q, want %q", i, r.got[i], want[i])
		}
	}
}
