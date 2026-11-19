package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// EnqueueFollowup records a message to run after the current turn finishes.
// It is not steering: the manager will not see this text until a later turn.
// Nothing running is idle, not a silent queue that never flushes.
func (e *Engine) EnqueueFollowup(threadID, text string) (*store.Followup, error) {
	return e.enqueueFollowupInput(threadID, UserInput{Text: text})
}

func (e *Engine) enqueueFollowupInput(threadID string, in UserInput) (*store.Followup, error) {
	text := strings.TrimSpace(in.Text)
	if IsImageCommand(text) {
		return nil, ErrBusy
	}
	if _, err := e.store.GetThread(threadID); err != nil {
		return nil, err
	}
	if _, _, err := e.hydrateUserInput(threadID, in, "engine: an empty follow-up has nothing to wait for"); err != nil {
		return nil, err
	}
	rt := e.deliveryRuntime(threadID)
	if rt == nil {
		return nil, ErrIdle
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.running || rt.userStopped || rt.abandoned {
		return nil, ErrIdle
	}
	refs, err := e.saveInputImages(threadID, in.Images)
	if err != nil {
		return nil, err
	}
	item, err := e.store.EnqueueFollowupInput(threadID, text, in.Files, refs)
	if err != nil {
		e.removeFollowupImages(threadID, refs)
	}
	return item, err
}

// ListFollowups returns waiting messages in the order they were typed.
func (e *Engine) ListFollowups(threadID string) ([]store.Followup, error) {
	if _, err := e.store.GetThread(threadID); err != nil {
		return nil, err
	}
	return e.store.ListFollowups(threadID)
}

// DeleteFollowup drops one waiting message. Missing is not found.
func (e *Engine) DeleteFollowup(threadID, id string) error {
	if _, err := e.store.GetThread(threadID); err != nil {
		return err
	}
	item, err := e.store.TakeFollowup(threadID, id)
	if err == nil {
		e.removeFollowupImages(threadID, item.Images)
	}
	return err
}

// RequeueFollowup saves an edited waiting message at the back of the FIFO.
// The queue can outlive the turn that created it (Stop leaves rows), so this
// does not require a running turn.
func (e *Engine) RequeueFollowup(threadID, id, text string) (*store.Followup, error) {
	if IsImageCommand(text) {
		return nil, ErrBusy
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("engine: an empty follow-up has nothing to wait for")
	}
	if _, err := e.store.GetThread(threadID); err != nil {
		return nil, err
	}
	return e.store.RequeueFollowup(threadID, id, text)
}

// SteerFollowup pulls a waiting message into the running turn at the next
// model boundary. It does not cancel an in-flight tool. A nil turn means
// that injection landed. When nothing is running, the same click starts the
// text as the next turn: Stop leaves the queue, and this button is the user
// saying send it now. A start that loses the slot puts the row back.
func (e *Engine) SteerFollowup(threadID, id string) (*store.Turn, error) {
	f, err := e.store.TakeFollowup(threadID, id)
	if err != nil {
		return nil, err
	}
	in, err := e.followupInput(f)
	if err == nil {
		err = e.SteerInput(threadID, in)
		if err == nil {
			return nil, nil
		}
		if errors.Is(err, ErrIdle) {
			var turn *store.Turn
			turn, err = e.StartTurnInput(threadID, in)
			if err == nil {
				return turn, nil
			}
		}
	}
	e.restoreFollowup(f)
	return nil, err
}

func (e *Engine) restoreFollowup(f *store.Followup) {
	if uerr := e.store.UnshiftFollowup(f); uerr != nil {
		e.log.Warn("could not restore a follow-up after a failed steer",
			"thread", f.ThreadID, "followup", f.ID, "err", uerr)
	}
}

// flushFollowup starts the oldest waiting message as the next turn after a
// completed or failed turn, handing off to explicitly queued human work.
// Stop, cancellation and restart leave the queue where it is.
// A late steer that already claimed the next turn gets ErrBusy; the row
// goes back to the front so it runs after that one.
func (rt *runtime) flushFollowup(status string) bool {
	if (status != store.TurnDone && status != store.TurnError) || rt.engine.restarting() {
		return false
	}
	item, err := rt.engine.store.PopFollowup(rt.threadID)
	if err != nil {
		rt.engine.log.Warn("could not pop a follow-up", "thread", rt.threadID, "err", err)
		return false
	}
	if item == nil {
		return false
	}
	in, err := rt.engine.followupInput(item)
	if err != nil {
		rt.engine.restoreFollowup(item)
		rt.engine.log.Warn("could not load a queued follow-up", "thread", rt.threadID, "followup", item.ID, "err", err)
		return false
	}
	var startErr error
	for attempt := 0; attempt < 8; attempt++ {
		_, startErr = rt.engine.StartTurnInput(rt.threadID, in)
		if startErr == nil {
			return true
		}
		if errors.Is(startErr, ErrBusy) {
			break
		}
		if !strings.Contains(startErr.Error(), "database is locked") &&
			!strings.Contains(startErr.Error(), "SQLITE_BUSY") {
			break
		}
		time.Sleep(time.Duration(20*(attempt+1)) * time.Millisecond)
	}
	if uerr := rt.engine.store.UnshiftFollowup(item); uerr != nil {
		rt.engine.log.Warn("could not put a follow-up back after it failed to start",
			"thread", rt.threadID, "followup", item.ID, "err", uerr)
	}
	if !errors.Is(startErr, ErrBusy) {
		rt.engine.log.Warn("could not start the next follow-up",
			"thread", rt.threadID, "err", startErr)
	}
	return false
}
