package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/backup"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func TestThreadWorkPromotionUsesTaggedVisibleSnapshot(t *testing.T) {
	h := newHarness(t)
	projectID := h.newProject(map[string]any{"name": "Work ownership"})["id"].(string)
	first := newWorkThread(t, h, "Work source", projectID)
	second := newWorkThread(t, h, "Another conversation", projectID)
	standalone := newWorkThread(t, h, "Independent source", "")
	private := addPrivateWorkNode(t, h, first, "A private premise")
	leaf := addPrivateWorkNode(t, h, first, "Dependent fact", private.ID)

	path := "/api/threads/" + first + "/work"
	initial, tag := readWork(t, h, first)
	if initial.ThreadRevision != 2 || initial.ProjectRevision != 0 || len(initial.Nodes) != 2 || initial.Nodes[0].Scope != store.WorkScopeThread {
		t.Fatalf("own snapshot = %+v", initial)
	}
	peer, peerTag := readWork(t, h, second)
	if len(peer.Nodes) != 0 || peer.ProjectID != projectID || peer.ThreadRevision != 0 || peer.ProjectRevision != 0 {
		t.Fatalf("private records leaked into another conversation: %+v", peer)
	}
	projectPath := "/api/projects/" + projectID + "/nuwa"
	if shared := h.json(http.MethodGet, projectPath, nil, http.StatusOK); len(shared["nodes"].([]any)) != 0 {
		t.Fatalf("private records leaked into the project: %v", shared)
	}

	post := func(path, tag string, ids []string, status int) (map[string]any, string) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"ids": ids})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.srv.URL+path+"/promote", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if tag != "" {
			req.Header.Set("If-Match", tag)
		}
		response, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var answer map[string]any
		if err := json.NewDecoder(response.Body).Decode(&answer); err != nil || response.StatusCode != status {
			t.Fatalf("POST %s: status=%d want=%d answer=%v err=%v", path, response.StatusCode, status, answer, err)
		}
		return answer, response.Header.Get("ETag")
	}

	ids := []string{leaf.ID}
	for _, bad := range []string{"", "*", "broken"} {
		if answer, _ := post(path, bad, ids, http.StatusBadRequest); answer["code"] != "work_invalid" {
			t.Fatalf("bad tag answer=%v", answer)
		}
	}
	if answer, _ := post("/api/threads/"+second+"/work", peerTag, ids, http.StatusBadRequest); answer["code"] != "work_invalid" {
		t.Fatalf("foreign root answer=%v", answer)
	}
	solo, soloTag := readWork(t, h, standalone)
	if solo.ProjectID != "" || solo.ProjectRevision != 0 {
		t.Fatalf("standalone project = %+v", solo)
	}
	if answer, _ := post("/api/threads/"+standalone+"/work", soloTag, ids, http.StatusBadRequest); answer["code"] != "work_invalid" {
		t.Fatalf("standalone answer=%v", answer)
	}
	if _, err := backup.AdvanceGeneration(h.app.Config.DataDir()); err != nil {
		t.Fatal(err)
	}
	if answer, _ := post(path, tag, ids, http.StatusConflict); answer["code"] != "work_conflict" {
		t.Fatalf("restored database precondition answer=%v", answer)
	}
	_, currentTag := readWork(t, h, first)
	answer, promotedTag := post(path, currentTag, ids, http.StatusOK)
	if len(answer["nodes"].([]any)) != 2 || answer["data_generation"] != float64(1) || promotedTag == "" || currentTag == promotedTag {
		t.Fatalf("promotion closure did not return fresh identity: answer=%v tag=%q", answer, promotedTag)
	}
	if answer, _ := post(path, currentTag, ids, http.StatusConflict); answer["code"] != "work_conflict" {
		t.Fatalf("stale revision answer=%v", answer)
	}
	after, _ := readWork(t, h, second)
	if after.ThreadRevision != 0 || after.ProjectRevision == 0 || len(after.Nodes) != 2 {
		t.Fatalf("peer snapshot after promotion=%+v", after)
	}
	for _, node := range after.Nodes {
		if node.Scope != store.WorkScopeProject || node.Version != 2 || node.ProjectID != projectID {
			t.Fatalf("promoted node identity=%+v", node)
		}
	}
	if shared := h.json(http.MethodGet, projectPath, nil, http.StatusOK); len(shared["nodes"].([]any)) != 2 {
		t.Fatalf("legacy project view missed promoted records: %v", shared)
	}
}

func TestThreadWorkSSEInvalidatesWithoutChatCursor(t *testing.T) {
	h := newHarness(t)
	threadID := newWorkThread(t, h, "Independent work stream", "")
	persisted := store.Event{ThreadID: threadID, Kind: "user", Text: "Earlier chat content"}
	if err := h.app.Store.AppendEvent(&persisted); err != nil {
		t.Fatal(err)
	}
	path := "/api/threads/" + threadID
	direct := openWorkEvents(t, h, path+"/work/events")
	chat := openWorkEvents(t, h, path+"/events?work=1")
	for _, next := range []func() workFrame{direct, chat} {
		frame := next()
		if frame.id != "" || frame.version.ThreadID != threadID || frame.version.ProjectID != "" ||
			frame.version.ThreadRevision != 0 || frame.version.ProjectRevision != 0 {
			t.Fatalf("initial work invalidation must not carry a chat cursor: %+v", frame)
		}
	}
	addPrivateWorkNode(t, h, threadID, "A durable fact")
	for _, next := range []func() workFrame{direct, chat} {
		frame := next()
		if frame.id != "" || frame.version.ThreadRevision != 1 || frame.version.ProjectRevision != 0 {
			t.Fatalf("committed work did not invalidate its conversation: %+v", frame)
		}
	}
	if snapshot, _ := readWork(t, h, threadID); len(snapshot.Nodes) != 1 {
		t.Fatalf("work event was not backed by a visible record: %+v", snapshot)
	}
	for _, suffix := range []string{"", "/events"} {
		h.json(http.MethodGet, "/api/threads/th_missing/work"+suffix, nil, http.StatusNotFound)
	}
}

type workFrame struct {
	id      string
	version store.WorkRevision
}

func openWorkEvents(t *testing.T, h *harness, path string) func() workFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET %s status=%d headers=%v", path, response.StatusCode, response.Header)
	}
	reader := bufio.NewReader(response.Body)
	return func() workFrame {
		t.Helper()
		var name, id, data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if name == "nuwa_updated" {
					frame := workFrame{id: id}
					if err := json.Unmarshal([]byte(data), &frame.version); err != nil {
						t.Fatal(err)
					}
					return frame
				}
				name, id, data = "", "", ""
			}
		}
	}
}

func readWork(t *testing.T, h *harness, threadID string) (store.WorkSnapshot, string) {
	t.Helper()
	response := h.do(http.MethodGet, "/api/threads/"+threadID+"/work", nil)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	var snapshot store.WorkSnapshot
	tag := response.Header.Get("ETag")
	if err != nil || response.StatusCode != http.StatusOK || tag == "" || json.Unmarshal(raw, &snapshot) != nil {
		t.Fatalf("work response status=%d tag=%q body=%s err=%v", response.StatusCode, tag, raw, err)
	}
	return snapshot, tag
}

func newWorkThread(t *testing.T, h *harness, title, projectID string) string {
	t.Helper()
	row := h.json(http.MethodPost, "/api/threads",
		map[string]any{"title": title, "project_id": projectID}, http.StatusCreated)
	return row["thread"].(map[string]any)["id"].(string)
}

func addPrivateWorkNode(t *testing.T, h *harness, threadID, title string, deps ...string) store.NuwaNode {
	t.Helper()
	turn := store.Turn{ThreadID: threadID, WorkLedger: true}
	if err := h.app.Store.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	node := store.NuwaNode{
		Scope: store.WorkScopeThread, ThreadID: threadID, TurnID: turn.ID, AgentID: "manager",
		Kind: "fact", Title: title, DependsOn: deps,
	}
	if err := h.app.Store.UpsertNuwaNode(t.Context(), &node, 0); err != nil {
		t.Fatal(err)
	}
	return node
}
