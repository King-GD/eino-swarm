package server_test

import (
	"net/http"
	"strings"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
	"testing"
)

func TestProjectDistillRejectsForeignConversationAndKeepsEmptyProjectIdle(t *testing.T) {
	h := newHarness(t)
	project := h.newProject(map[string]any{"name": "Subject"})
	base := "/api/projects/" + project["id"].(string)
	if status := h.json(http.MethodGet, base+"/distill", nil, http.StatusOK); status["state"] != "idle" {
		t.Fatalf("initial status: %v", status)
	}
	if status := h.json(http.MethodPost, base+"/distill", map[string]any{
		"scope": "project", "enable_knowledge": true,
	}, http.StatusOK); status["state"] != "no_work" {
		t.Fatalf("empty project started work: %v", status)
	}
	h.json(http.MethodPost, base+"/distill", map[string]any{
		"scope": "thread", "thread_id": "th_missing",
	}, http.StatusNotFound)
	h.json(http.MethodPost, base+"/distill", map[string]any{
		"scope": "project", "thread_id": "th_missing",
	}, http.StatusBadRequest)
	h.json(http.MethodGet, "/api/projects/pj_missing/distill", nil, http.StatusNotFound)
	h.json(http.MethodPost, "/api/projects/pj_missing/distill", map[string]any{"scope": "project"}, http.StatusNotFound)
	h.json(http.MethodPost, base+"/distill", "not an object", http.StatusBadRequest)
	h.json(http.MethodPost, base+"/distill", map[string]any{"scope": "latest_turn"}, http.StatusBadRequest)
	h.json(http.MethodPost, base+"/distill", map[string]any{"scope": "unknown"}, http.StatusBadRequest)
	foreignID := h.newThread()
	h.json(http.MethodPost, base+"/distill", map[string]any{"scope": "thread", "thread_id": foreignID}, http.StatusNotFound)
	if status := h.json(http.MethodGet, base+"/knowledge", nil, http.StatusOK); status["enabled"] != false {
		t.Fatalf("declined request created a schema: %v", status)
	}
	disabled := h.newProject(map[string]any{"name": "Disabled destinations", "memory_enabled": false})["id"].(string)
	th := h.json(http.MethodPost, "/api/threads", map[string]any{"title": "Retained source", "project_id": disabled}, http.StatusCreated)["thread"].(map[string]any)
	turn := &store.Turn{ThreadID: th["id"].(string), ProjectID: disabled, Status: store.TurnDone}
	if err := h.app.Store.CreateTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.AppendEvent(&store.Event{ThreadID: turn.ThreadID, TurnID: turn.ID, Kind: engine.KindUser, Text: "Retained input."}); err != nil {
		t.Fatal(err)
	}
	if result := h.json(http.MethodPost, "/api/projects/"+disabled+"/distill", map[string]any{"scope": "project"}, http.StatusConflict); result["code"] != "distillation_disabled" {
		t.Fatalf("missing actionable admission error: %v", result)
	}

}

func TestProjectDistillOneReviewWritesNotesSkillAndKnowledgeWithRealTrace(t *testing.T) {
	h := newHarness(t)
	project := h.newProject(map[string]any{"name": "Subject", "memory_enabled": true})
	id := project["id"].(string)
	thread := h.json(http.MethodPost, "/api/threads", map[string]any{"title": "Study", "project_id": id}, http.StatusCreated)["thread"].(map[string]any)
	threadID := thread["id"].(string)
	st := h.app.Store
	turn := store.Turn{ThreadID: threadID, ProjectID: id, Status: store.TurnDone, ProviderID: thread["provider_id"].(string)}
	if err := st.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	event := &store.Event{ThreadID: threadID, TurnID: turn.ID, Kind: engine.KindUser, Text: "Preserve the measured observation and retain the reusable procedure."}
	if err := st.AppendEvent(event); err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + id + "/distill"
	started := h.json(http.MethodPost, path, map[string]any{"scope": "thread", "thread_id": threadID, "enable_knowledge": true}, http.StatusAccepted)
	var result map[string]any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		result = h.json(http.MethodGet, path, nil, http.StatusOK)
		if result["state"] != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result["review_id"] != started["review_id"] || result["state"] != "completed" {
		t.Fatalf("review did not complete: %v", result)
	}
	if result["thread_id"] != threadID || result["turn_id"] != turn.ID {
		t.Fatalf("wrong trace anchor: %v", result)
	}
	if changes, ok := result["changes"].([]any); !ok || len(changes) != 2 {
		t.Fatalf("missing actual memory and skill receipts: %v", result)
	}
	knowledge, ok := result["knowledge"].([]any)
	if !ok || len(knowledge) != 1 {
		t.Fatalf("missing actual knowledge receipt: %v", result)
	}
	pageID := knowledge[0].(map[string]any)["id"].(string)
	page := h.json(http.MethodGet, "/api/projects/"+id+"/knowledge/pages/"+pageID, nil, http.StatusOK)
	source := page["sources"].([]any)[0].(map[string]any)
	if source["thread_id"] != threadID || source["seq"] != float64(event.Seq) || source["classification"] != "primary" ||
		source["available"] != true || source["timestamp"] == nil || page["updated_turn"] != turn.ID {
		t.Fatalf("invalid knowledge provenance: %v", page)
	}
	notes, err := h.app.Engine.ProjectMemory(id).Read()
	if err != nil || len(notes.Entries) != 1 || !strings.Contains(notes.Entries[0], event.Text) {
		t.Fatalf("notes did not persist: %+v, %v", notes, err)
	}
	skills, err := h.app.Engine.ProjectMemory(id).ListSkills()
	if err != nil || len(skills) != 1 {
		t.Fatalf("skill did not persist: %v, %v", skills, err)
	}
	calls, err := st.ListLLMCalls(turn.ID)
	if err != nil || len(calls) != 1 || calls[0].AgentID != engine.ReviewAgentID {
		t.Fatalf("expected one actual reviewer call on the original trace: %v, %v", calls, err)
	}
}
