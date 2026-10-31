package explore_test

import (
	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke/keytest"
)

func keyMsg(k string) tea.Msg { return keytest.Msg(k, false, false) }
