package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/slash"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"github.com/cloudwego/eino/schema"
)

type DeliveryMode string

const (
	DeliveryQueue     DeliveryMode = "queue"
	DeliverySteer     DeliveryMode = "steer"
	DeliveryInterrupt DeliveryMode = "interrupt"
)

var (
	ErrDeliveryTurnChanged = errors.New("the target turn has ended or changed; your message was kept")
	ErrInvalidDelivery     = errors.New("invalid follow-up delivery")
)

type DeliveryReceipt struct {
	Turn      *store.Turn     `json:"turn,omitempty"`
	Followup  *store.Followup `json:"followup,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	SteerSeq  int64           `json:"steer_seq,omitempty"`
	Preempted bool            `json:"preempted,omitempty"`
}

// DeliverComposerInput is shared by PC HTTP and the paired phone RPC. The
// expected turn is mandatory for insertion, including a selected queue row.
func (e *Engine) DeliverComposerInput(threadID string, mode DeliveryMode, expectedTurn, followupID string, in UserInput) (DeliveryReceipt, error) {
	switch mode {
	case DeliveryQueue:
		if followupID != "" || expectedTurn != "" {
			return DeliveryReceipt{}, ErrInvalidDelivery
		}
	case DeliverySteer, DeliveryInterrupt:
		if expectedTurn == "" || (followupID != "" && (in.Text != "" || len(in.Files) != 0 || len(in.Images) != 0)) {
			return DeliveryReceipt{}, ErrInvalidDelivery
		}
	default:
		return DeliveryReceipt{}, ErrInvalidDelivery
	}
	_, goal := slash.Lookup(in.Text, "goal")
	_, plan := slash.Lookup(in.Text, "plan")
	if in.HistoryReport || goal || plan || IsImageCommand(in.Text) {
		return DeliveryReceipt{}, ErrInvalidDelivery
	}
	done, err := e.beginAdmission()
	if err != nil {
		return DeliveryReceipt{}, err
	}
	defer done()
	if mode != DeliveryQueue {
		return e.deliverSteer(threadID, expectedTurn, followupID, in, mode == DeliveryInterrupt)
	}
	item, err := e.enqueueFollowupInput(threadID, in)
	if !errors.Is(err, ErrIdle) {
		return DeliveryReceipt{Followup: item}, err
	}
	turn, err := e.startTurnInput(threadID, in)
	if errors.Is(err, ErrBusy) {
		item, err = e.enqueueFollowupInput(threadID, in)
		return DeliveryReceipt{Followup: item}, err
	}
	return DeliveryReceipt{Turn: turn}, err
}

func (e *Engine) deliveryRuntime(threadID string) *runtime {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runtimes[threadID]
}

func (e *Engine) followupInput(item *store.Followup) (UserInput, error) {
	in := UserInput{Text: item.Text, Files: item.Files, storedImages: item.Images, queuedFollowup: true}
	for _, image := range item.Images {
		data, mime, err := e.ReadInputImage(item.ThreadID, image.ID)
		if err != nil {
			return UserInput{}, fmt.Errorf("load queued image %s: %w", image.ID, err)
		}
		in.Images = append(in.Images, ImageInput{Name: image.Name, MIME: mime, Data: data})
	}
	return in, nil
}

func (e *Engine) removeFollowupImages(threadID string, refs []store.ImageRef) {
	for _, ref := range refs {
		if !safeImageID(ref.ID) {
			continue
		}
		if err := os.Remove(filepath.Join(e.cfg.ThreadInputsDir(threadID), ref.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			e.log.Warn("could not remove a queued image", "thread", threadID, "image", ref.ID, "err", err)
		}
	}
}

func (e *Engine) deliverSteer(threadID, expectedTurn, followupID string, in UserInput, interrupt bool) (DeliveryReceipt, error) {
	rt := e.deliveryRuntime(threadID)
	if rt == nil {
		return DeliveryReceipt{}, ErrDeliveryTurnChanged
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.running || rt.turnID != expectedTurn || rt.reg == nil || rt.userStopped || rt.abandoned {
		return DeliveryReceipt{}, ErrDeliveryTurnChanged
	}
	if rt.imageOnly {
		return DeliveryReceipt{}, ErrBusy
	}
	if rt.ask != nil {
		return DeliveryReceipt{}, ErrAskMismatch
	}
	var queued *store.Followup
	if followupID != "" {
		var err error
		queued, err = e.store.GetFollowup(threadID, followupID)
		if err != nil {
			return DeliveryReceipt{}, err
		}
		in, err = e.followupInput(queued)
		if err != nil {
			return DeliveryReceipt{}, err
		}
	}
	text, files, err := e.hydrateUserInput(threadID, in, "engine: nothing to insert")
	if err != nil {
		return DeliveryReceipt{}, err
	}
	refs := in.storedImages
	if queued == nil {
		refs, err = e.saveInputImages(threadID, in.Images)
		if err != nil {
			return DeliveryReceipt{}, err
		}
	}
	event := store.Event{ThreadID: threadID, TurnID: expectedTurn, AgentID: swarm.DefaultManagerID, Role: "human", Kind: KindSteer, Text: text, Images: refs}
	// Share the record lock with ordinary live events so a model request
	// consuming this input cannot publish before the durable steer receipt.
	e.recordMu.Lock()
	err = e.store.CommitSteerDelivery(&event, store.Message{Role: string(schema.User), Content: "[steer] " + text, Images: refs, Source: store.MessageSourceHuman}, files, queued)
	if err == nil {
		e.reindex(threadID)
		e.broadcast(event)
	}
	e.recordMu.Unlock()
	if err != nil {
		if queued == nil {
			e.removeFollowupImages(threadID, refs)
		}
		return DeliveryReceipt{}, err
	}
	msg := BuildUserMessage("[steer] "+text, in.Images)
	withMessageSource(msg, store.MessageSourceHuman)
	swarm.SetSteerSeq(msg, event.Seq)
	var preempted bool
	if interrupt {
		_, preempted = rt.reg.SteerManagerMessageNow(msg)
	} else {
		rt.reg.SteerManagerMessage(msg)
	}
	if preempted {
		e.record(store.Event{ThreadID: threadID, TurnID: expectedTurn, AgentID: swarm.DefaultManagerID, Kind: KindSteerPreempted})
	}
	if rt.answerCancel != nil {
		rt.answerCancel()
	}
	rt.signalContinueLocked(true)
	return DeliveryReceipt{TurnID: expectedTurn, SteerSeq: event.Seq, Preempted: preempted}, nil
}
