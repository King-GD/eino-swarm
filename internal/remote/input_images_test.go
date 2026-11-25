package remote

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestPCInputImagesReachPairedPhoneLiveAndInHistory(t *testing.T) {
	t.Setenv("ZWAI_MOCK_WORKER_DELAY_MS", "2500")
	e := testEngine(t)
	thread, err := e.CreateThread("Images from PC", "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.CreateThread("Other conversation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	pixels := image.NewNRGBA(image.Rect(0, 0, 181, 91))
	var original, second bytes.Buffer
	pngEncoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := pngEncoder.Encode(&original, pixels); err != nil || original.Len() <= FileChunkRaw {
		t.Fatalf("fixture must be a valid multi-chunk PNG: %d bytes, %v", original.Len(), err)
	}
	pixels.SetNRGBA(5, 5, color.NRGBA{R: 255, A: 255})
	if err := pngEncoder.Encode(&second, pixels); err != nil {
		t.Fatal(err)
	}
	images := []engine.ImageInput{
		{Name: "first.png", MIME: "image/png", Data: original.Bytes()},
		{Name: "second.png", MIME: "image/png", Data: second.Bytes()},
	}
	relay := newFollowupRelay(t, e)
	request := func(id, op string, before int64) Response {
		return relay.rpc(t, Request{ID: id, Op: op, ThreadID: thread.ID, Before: before})
	}
	if response := request("watch", OpWatch, 0); !response.OK || response.Op != OpReady {
		t.Fatalf("phone watch: %+v", response)
	}
	turn, err := e.StartTurnInput(thread.ID, engine.UserInput{Text: "Two images", Images: images})
	if err != nil {
		t.Fatal(err)
	}
	user := relay.userEvent(t, "Two images")
	ids := user.ImageIDs
	if !user.HasImages || len(ids) != 2 || ids[0] == ids[1] || ids[0] == "" || ids[1] == "" {
		t.Fatalf("live phone event must identify both input images: %+v", user)
	}
	if err := e.SteerInput(thread.ID, engine.UserInput{Images: images[:1]}); err != nil {
		t.Fatal(err)
	}
	var steer *EventView
	for deadline := time.After(5 * time.Second); steer == nil; {
		event := relay.receive(t, deadline).Event
		if event != nil && event.Kind == engine.KindSteer {
			steer = event
		}
	}
	steerIDs := steer.ImageIDs
	if len(steerIDs) != 1 || steerIDs[0] == "" || steerIDs[0] == ids[0] {
		t.Fatalf("image-only steer lost its input ref: %+v", steer)
	}
	if err := e.Interrupt(thread.ID); err != nil {
		t.Fatal(err)
	}
	waitFollowupRelayIdle(t, e, thread.ID, turn.ID)
	if resp := request("unwatch", OpUnwatch, 0); !resp.OK {
		t.Fatalf("unwatch: %+v", resp)
	}
	for _, resp := range []Response{
		request("reopen", OpWatch, 0),
		request("earlier", OpLog, steer.Seq+1),
		relay.rpc(t, Request{ID: "compact", Op: OpLog, ThreadID: thread.ID, Before: steer.Seq + 1, CompactHistory: true}),
	} {
		seen := map[int64][]string{}
		for _, ev := range resp.Events {
			seen[ev.Seq] = ev.ImageIDs
		}
		if !resp.OK || !reflect.DeepEqual(seen[user.Seq], ids) || !reflect.DeepEqual(seen[steer.Seq], steerIDs) {
			t.Fatalf("phone replay lost the input image identity: %s, %+v", resp.ID, seen)
		}
	}

	expected := map[string][]byte{ids[0]: original.Bytes(), ids[1]: second.Bytes(), steerIDs[0]: original.Bytes()}
	for id, originalBytes := range expected {
		var received []byte
		for i := 0; ; i++ {
			if i > 2 {
				t.Fatal("image chunks did not terminate")
			}
			offset := int64(len(received))
			resp := relay.rpc(t, Request{ID: id, Op: OpInputImageChunk, ThreadID: thread.ID, ImageID: id, Before: offset})
			c := resp.FileChunk
			if !resp.OK || c == nil || c.Path != id || c.MIME != "image/png" || c.Size != int64(len(originalBytes)) || c.Offset != offset {
				t.Fatalf("wrong image chunk: ok=%v code=%s error=%s", resp.OK, resp.Code, resp.Error)
			}
			part, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil || len(part) == 0 || len(part) > FileChunkRaw || c.NextOffset != offset+int64(len(part)) {
				t.Fatalf("bad chunk at %d: %d bytes, next %d, %v", offset, len(part), c.NextOffset, err)
			}
			received = append(received, part...)
			if !c.More {
				break
			}
		}
		decoded, err := png.Decode(bytes.NewReader(received))
		if err != nil {
			t.Fatalf("phone image could not be decoded: %v", err)
		}
		if decoded.Bounds() != pixels.Bounds() || !bytes.Equal(received, originalBytes) {
			t.Fatalf("phone image changed: %v", decoded.Bounds())
		}
	}
	const linkedID = "img_1058"
	const nonImageID = "img_2070"
	root := e.Config().ThreadInputsDir(thread.ID)
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, original.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, nonImageID), []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, thread, image, code string
		before                  int64
	}{
		{"wrong-thread", other.ID, ids[0], "not_found", 0},
		{"path", thread.ID, "img_../" + ids[0], "not_found", 0},
		{"non-image", thread.ID, nonImageID, "not_found", 0},
		{"negative", thread.ID, ids[0], "bad_request", -1},
		{"past-end", thread.ID, ids[0], "bad_request", int64(original.Len() + 1)},
	} {
		resp := relay.rpc(t, Request{ID: tc.id, Op: OpInputImageChunk, ThreadID: tc.thread, ImageID: tc.image, Before: tc.before})
		if resp.OK || resp.Code != tc.code || resp.FileChunk != nil {
			t.Fatalf("accepted %s: %+v", tc.id, resp)
		}
	}
	t.Run("stored symlink", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(root, linkedID)); err != nil {
			if os.IsPermission(err) {
				t.Skipf("cannot create private test symlink: %v", err)
			}
			t.Fatal(err)
		}
		resp := relay.rpc(t, Request{ID: "symlink", Op: OpInputImageChunk, ThreadID: thread.ID, ImageID: linkedID})
		if resp.OK || resp.Code != "not_found" || resp.FileChunk != nil {
			t.Fatalf("returned bytes outside the conversation: %+v", resp)
		}
	})
}

func TestInputImageMarkersKeepTheEventWithinThePhoneFrame(t *testing.T) {
	ev := store.Event{Kind: engine.KindUser, Seq: 1, Images: []store.ImageRef{
		{ID: strings.Repeat("1", plaintextBudget())},
	}}
	payload, ok := encodeEventPush("relay", "session", "thread", ev, config.RemoteConfig{})
	if !ok || len(payload) > plaintextBudget() || !bytes.Contains(payload, []byte(`"has_images":true`)) ||
		bytes.Contains(payload, []byte(`"image_ids"`)) {
		t.Fatal("oversized historical image metadata must leave a bounded event with a placeholder")
	}
}

func TestInputImageChunkReportsDescriptorErrorsWithoutBytes(t *testing.T) {
	req := Request{ID: "chunk", ThreadID: "thread", ImageID: "img_ab"}
	for _, tc := range []struct {
		name string
		open func() (*os.File, error)
	}{
		{"closed descriptor", func() (*os.File, error) {
			f, err := os.CreateTemp(t.TempDir(), "image")
			if err != nil {
				return nil, err
			}
			return f, f.Close()
		}},
		{"unreadable descriptor", func() (*os.File, error) {
			// Stat works on a write-only file, but ReadAt must fail explicitly.
			return os.OpenFile(filepath.Join(t.TempDir(), "image"), os.O_CREATE|os.O_WRONLY, 0o600)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := tc.open()
			if err != nil {
				t.Fatal(err)
			}
			resp := handleImageChunk(func(string, string) (*os.File, string, error) {
				return f, "image/png", nil
			}, req, "relay", "session")
			if resp.OK || resp.Error == "" || resp.FileChunk != nil {
				t.Fatalf("expected a read failure without pixels: %+v", resp)
			}
		})
	}
}
