package engine

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	swarm "github.com/LubyRuffy/eino-swarm"
	"gorm.io/gorm"
)

func TestComposerDeliveryReachesTheIntendedModelRequest(t *testing.T) {
	for _, mode := range []DeliveryMode{DeliveryQueue, DeliverySteer, DeliveryInterrupt} {
		t.Run(string(mode), func(t *testing.T) {
			entered := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			e, wire := newScheduleCompletionHTTPEngine(t, func(w http.ResponseWriter, r *http.Request, step int, request scheduleCompletionRequest) {
				if step == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, ": first model request is active\n\n")
					w.(http.Flusher).Flush()
					close(entered)
					select {
					case <-r.Context().Done():
						close(cancelled)
						return
					case <-release:
					}
					if mode == DeliverySteer {
						writeManagedExecHTTPCall(w, request, "first-tool", `{"command":"printf existing-step"}`)
						return
					}
				}
				writeContextFlowReply(w, request.Stream, map[string]any{"content": "Model request completed."}, "stop")
			})
			defer unblock()
			th, err := e.CreateThread("Send method test", "", "")
			if err != nil {
				t.Fatal(err)
			}
			first, err := e.StartTurn(th.ID, "Run the original task.")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the first provider request never began")
			}
			const text = "Use the human follow-up."
			var id, expected string
			if mode != DeliveryQueue {
				expected = first.ID
				kept, err := e.EnqueueFollowup(th.ID, "First queue row stays in line.")
				if err != nil {
					t.Fatal(err)
				}
				selected, err := e.EnqueueFollowup(th.ID, text)
				if err != nil {
					t.Fatal(err)
				}
				id = selected.ID
				_, err = e.DeliverComposerInput(th.ID, mode, "stale-turn", id, UserInput{})
				if !errors.Is(err, ErrDeliveryTurnChanged) {
					t.Fatalf("changed turn accepted: %v", err)
				}
				if item, err := e.store.GetFollowup(th.ID, kept.ID); err != nil || item.Text != kept.Text {
					t.Fatalf("the unrelated row was changed: %+v %v", item, err)
				}
			}
			in := UserInput{}
			if mode == DeliveryQueue {
				in.Text = text
				file, err := e.SaveUpload(th.ID, "sample.txt", func(path string) error { return os.WriteFile(path, []byte("evidence"), 0600) })
				if err != nil {
					t.Fatal(err)
				}
				in.Files = []string{file.RelPath}
			}
			receipt, err := e.DeliverComposerInput(th.ID, mode, expected, id, in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == DeliveryQueue {
				if receipt.Followup == nil || len(receipt.Followup.Files) != 1 || receipt.Turn != nil || receipt.SteerSeq != 0 {
					t.Fatalf("message was not queued with its file: %+v", receipt)
				}
			} else if receipt.TurnID != first.ID || receipt.SteerSeq <= 0 || receipt.Preempted != (mode == DeliveryInterrupt) {
				t.Fatalf("incorrect insertion receipt: %+v", receipt)
			}
			if mode == DeliveryInterrupt {
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatal("the active HTTP request was not interrupted")
				}
			} else {
				if len(wire.snapshot()) != 1 {
					t.Fatal("a follow-up unexpectedly opened a second model request")
				}
				select {
				case <-cancelled:
					t.Fatal("the current model request was unexpectedly cancelled")
				default:
				}
			}
			unblock()
			waitForTurn(t, e, first.ID)
			ordinaryEventually(t, func() bool { return len(wire.snapshot()) >= 2 }, "the follow-up did not reach the provider")
			waitSettled(t, e, th.ID)
			requests := wire.snapshot()
			if len(requests) < 2 {
				t.Fatalf("expected the follow-up to reach the provider, observed %d requests", len(requests))
			}
			if containsModelMessage(requests[0], text) || !containsModelMessage(requests[1], text) {
				t.Fatal("follow-up reached the wrong request")
			}
			events, err := e.Replay(th.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, ev := range events {
				if mode == DeliveryQueue && ev.Kind == KindUser && strings.Contains(ev.Text, text) {
					found = ev.TurnID != first.ID && strings.Contains(ev.Text, in.Files[0])
				} else if mode != DeliveryQueue && ev.Kind == swarm.NotifySteerConsumed.String() && ev.Text == strconv.FormatInt(receipt.SteerSeq, 10) {
					found = ev.TurnID == first.ID
				}
			}
			if !found {
				t.Fatal("missing durable input receipt on the correct turn")
			}
		})
	}
}

func containsModelMessage(req scheduleCompletionRequest, text string) bool {
	for _, message := range req.Messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func TestSteerDeliveryRollsBackTheSelectedQueueRowOnStorageFailure(t *testing.T) {
	e := newTestEngine(t)
	th, err := e.CreateThread("Storage failure", "", "")
	if err != nil {
		t.Fatal(err)
	}
	release := holdTurn(t, e, th.ID)
	defer release()
	turn := e.Status(th.ID).TurnID
	file, err := e.SaveUpload(th.ID, "sample.txt", func(path string) error { return os.WriteFile(path, []byte("evidence"), 0600) })
	if err != nil {
		t.Fatal(err)
	}
	queued, err := e.DeliverComposerInput(th.ID, DeliveryQueue, "", "", UserInput{Text: "Keep me queued", Files: []string{file.RelPath}})
	if err != nil || queued.Followup == nil {
		t.Fatalf("queue the file: %+v %v", queued, err)
	}
	storageErr := errors.New("message storage is unavailable")
	if err := e.store.DB().Callback().Create().Before("gorm:create").Register("fail-delivery-message", func(tx *gorm.DB) {
		if tx.Statement.Table == "messages" {
			_ = tx.AddError(storageErr)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err = e.DeliverComposerInput(th.ID, DeliveryInterrupt, turn, queued.Followup.ID, UserInput{})
	if !errors.Is(err, storageErr) {
		t.Fatalf("expected original storage error, got %v", err)
	}
	rows, err := e.ListFollowups(th.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != queued.Followup.ID || len(rows[0].Files) != 1 {
		t.Fatalf("the queue claim was not rolled back: %+v %v", rows, err)
	}
	files, err := e.store.ListAttachments(th.ID)
	if err != nil || len(files) != 1 || files[0].TurnID != "" {
		t.Fatalf("attachment binding was not rolled back: %+v %v", files, err)
	}
	events, _ := e.Replay(th.ID, 0)
	for _, ev := range events {
		if ev.Kind == KindSteer || ev.Kind == KindSteerPreempted {
			t.Fatalf("failed delivery sent or interrupted the model: %+v", ev)
		}
	}
}

func TestQueuedImageKeepsItsBytesAndIdentityUntilDeliveryOrRemoval(t *testing.T) {
	e := newTestEngine(t)
	th, err := e.CreateThread("Queue attachments", "", "")
	if err != nil {
		t.Fatal(err)
	}
	release := holdTurn(t, e, th.ID)
	defer release()
	turn := e.Status(th.ID).TurnID
	in := UserInput{Images: []ImageInput{{Name: "preview.png", MIME: "image/png", Data: tinyPNG}}}
	keep, err := e.DeliverComposerInput(th.ID, DeliveryQueue, "", "", in)
	if err != nil || keep.Followup == nil || len(keep.Followup.Images) != 1 {
		t.Fatalf("expected an image-only queued message: %+v %v", keep, err)
	}
	remove, err := e.DeliverComposerInput(th.ID, DeliveryQueue, "", "", in)
	if err != nil || remove.Followup == nil {
		t.Fatalf("queue second image: %+v %v", remove, err)
	}
	if err := e.DeleteFollowup(th.ID, remove.Followup.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.ReadInputImage(th.ID, remove.Followup.Images[0].ID); err == nil {
		t.Fatal("removing a queued image left its private bytes")
	}
	got, err := e.DeliverComposerInput(th.ID, DeliveryInterrupt, turn, keep.Followup.ID, UserInput{})
	if err != nil || got.SteerSeq <= 0 || got.Preempted {
		t.Fatalf("expected image insertion without an active step to cancel: %+v %v", got, err)
	}
	data, mime, err := e.ReadInputImage(th.ID, keep.Followup.Images[0].ID)
	if err != nil || mime != "image/png" || !bytes.Equal(data, tinyPNG) {
		t.Fatalf("the delivered image lost its original identity or bytes: %s %v", mime, err)
	}
}
