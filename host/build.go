package host

import (
	"context"
	"fmt"
	"time"

	story "github.com/unixsuperhero/monogatari"
)

type playerBuilt struct{ player *player }

// Fixture.Build predates cancellation and may never return. Bound the host's
// wait, rather than the fixture goroutine; when an abandoned build eventually
// returns, that worker closes its own session. No worker touches host state.
func buildPlayer(parent context.Context, lib story.Library, st *story.Story, wait time.Duration) (*player, error) {
	return buildPlayerInSlot(parent, lib, st, wait, nil)
}

// A slot stays occupied until its fixture actually returns, including late
// cleanup. Waiting requests are cancellable and never create another fixture.
func buildPlayerInSlot(parent context.Context, lib story.Library, st *story.Story, wait time.Duration, slot chan struct{}) (*player, error) {
	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type outcome struct {
		p   *player
		err error
	}
	ready := make(chan outcome)
	go func() {
		if slot != nil {
			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		p, err := protectedPlayer(lib, st)
		select {
		case ready <- outcome{p, err}:
		case <-ctx.Done():
			if p != nil {
				p.Close()
			}
		}
	}()
	select {
	case got := <-ready:
		return got.p, got.err
	case <-ctx.Done():
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		return nil, fmt.Errorf("story %q took longer than %s to build", st.Name, wait)
	}
}

func protectedPlayer(lib story.Library, st *story.Story) (p *player, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("story %q could not start: fixture panicked: %v", st.Name, recovered)
		}
	}()
	return newPlayer(lib, st)
}
