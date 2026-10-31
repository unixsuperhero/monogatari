package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	story "github.com/unixsuperhero/monogatari"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/keytest"
)

func TestPlayStopAfterAction(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			h, lib := playHost(t)
			t.Cleanup(h.close)
			if h.idx.Runtime.Settings().Bool("play.stop-after-action") {
				t.Fatal("stop-after-action must default off")
			}
			if err := h.idx.Runtime.Settings().Set("play.stop-after-action", stop); err != nil {
				t.Fatal(err)
			}
			_, start := h.Update(startStory{story.Find(lib.Stories, "tutorials/tree-resets")})
			settlePlayerCommand(t, h, start)
			p := h.play
			for range 3 { // two setup keys, then the first action's reset
				h.Update(playTick{p.gen})
				if !p.auto {
					t.Fatal("paused during setup or before the first action")
				}
			}
			for want := 1; want <= 2; want++ {
				h.Update(playTick{p.gen})
				if !p.auto || p.action != 0 || p.at != want {
					t.Fatal("paused before the action's last frame was shown")
				}
			}
			old := p.gen
			_, cmd := h.Update(playTick{old})
			if p.action != 1 || p.at != 0 || p.d.S.R.Focus != "a3" || p.auto == stop {
				t.Fatalf("next reset: action=%d step=%d focus=%s auto=%v", p.action, p.at, p.d.S.R.Focus, p.auto)
			}
			if stop {
				if cmd != nil || !strings.Contains(screen(h), "paused") {
					t.Fatal("pause scheduled a timer or lacked its status")
				}
				h.Update(playTick{old})
				h.Update(playTick{p.gen})
				if p.at != 0 {
					t.Fatal("a timer advanced the paused reset point")
				}
				keytest.Press(h, "space")
				h.Update(playTick{old})
				if !p.auto || p.at != 0 {
					t.Fatal("space did not resume with a fresh timer")
				}
			}
			for range 2 {
				h.Update(playTick{p.gen})
			}
			if p.auto || !p.done() || p.d.S.R.Focus != "a5" || len(p.failures()) != 0 {
				t.Fatal("final action did not finish normally")
			}
		})
	}
}

func TestPlayStopAfterActionSettings(t *testing.T) {
	tool, out := testTool(t)
	if err := tool.Main([]string{"settings", "set", "play.stop-after-action", "true", "--yes"}); err != nil {
		t.Fatal(err, out.String())
	}
	h := browserHost(t)
	h.idx.Runtime.Config().Dir = tool.configDir()
	if !h.idx.Runtime.Settings().Bool("play.stop-after-action") {
		t.Fatal("player did not read the host CLI setting")
	}
	r := h.idx.Runtime
	if _, err := r.Execute(ochazuke.Request{Noun: "ochazuke.settings", Verb: "edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(ochazuke.Request{Noun: "ochazuke.settings", Verb: "section", Args: ochazuke.Arguments{"name": "play"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFocus("__settings/row/play.stop-after-action"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen(h), "play.stop-after-action") {
		t.Fatal("setting is missing from Settings", screen(h))
	}
}

// The fixture's output tick drives exactly one autoplay tick. This exercises
// real host playback deterministically, without sleeping on wall-clock timers.
func tutorialPlayerApp() story.App {
	return story.App{Name: "tutorial-player", Fixtures: []story.Fixture{{Name: "plain", Build: func(env story.Env) (story.Built, error) {
		stories, err := story.LoadDir(filepath.Join("testdata", "stories"))
		if err != nil {
			return story.Built{}, err
		}
		lib := story.Library{Apps: []story.App{rowsApp()}, Stories: stories}
		h, err := newHost(lib, "tutorial-player")
		if err != nil {
			return story.Built{}, err
		}
		h.idx.Runtime.Config().Disable()
		p, err := newPlayer(lib, story.Find(stories, "tutorials/independent-motions"))
		if err != nil {
			return story.Built{}, err
		}
		h.play, p.auto, p.gen = p, true, 1
		if err := h.idx.Runtime.OpenPage("play"); err != nil {
			h.close()
			return story.Built{}, err
		}
		return story.Built{Model: h.idx, Program: h, Close: h.close, Emit: func() bool {
			h.Update(playTick{h.play.gen})
			return true
		}}, nil
	}}}}
}

func TestStopAfterActionStory(t *testing.T) {
	Test(t, Config{Name: "tutorial-player", Apps: []story.App{tutorialPlayerApp()}, Stories: os.DirFS(filepath.Join("testdata", "browser-stories", "tutorials"))})
}
