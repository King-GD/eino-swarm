package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSteerFollowupHTTPRejectsInvalidRequestsWithoutConsumingQueue(t *testing.T) {
	h := newHarness(t)
	first, other := h.newThread(), h.newThread()
	f, err := h.app.Store.EnqueueFollowup(first, "only this conversation may consume me")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/threads/missing/followups/" + f.ID + "/steer",
		"/api/threads/" + first + "/followups/missing/steer",
		"/api/threads/" + other + "/followups/" + f.ID + "/steer",
	} {
		body := h.json(http.MethodPost, path, nil, http.StatusNotFound)
		message, ok := body["error"].(string)
		if !ok || message == "" || body["steered"] != nil || body["turn"] != nil {
			t.Fatalf("missing row looked accepted: %+v", body)
		}
	}
	// A malformed payload must not fall through to the bodyless send-now path.
	path := "/api/threads/" + first + "/followups/" + f.ID + "/steer"
	const privateBody = "private-body"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(privateBody))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.app.Server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || strings.Contains(res.Body.String(), privateBody) {
		t.Fatalf("malformed steer payload was not rejected safely (status %d)", res.Code)
	}
	rows, err := h.app.Store.ListFollowups(first)
	if err != nil || len(rows) != 1 || rows[0].ID != f.ID || rows[0].Text != f.Text {
		t.Fatalf("a rejected request consumed a row: %+v err=%v", rows, err)
	}
	for _, id := range []string{first, other} {
		turns, err := h.app.Store.ListTurns(id)
		if err != nil || len(turns) != 0 {
			t.Fatalf("rejected HTTP request started work: %+v err=%v", turns, err)
		}
	}
}

func TestSteerFollowupHTTPReportsStorageFailureAndKeepsTheQueue(t *testing.T) {
	h := newHarness(t)
	id := h.newThread()
	f, err := h.app.Store.EnqueueFollowup(id, "keep this request")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.app.Store.DB().Exec(`CREATE TRIGGER refuse_http_followup_delete BEFORE DELETE ON followups BEGIN SELECT RAISE(ABORT, 'HTTP followup delete unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	body := h.json(http.MethodPost, "/api/threads/"+id+"/followups/"+f.ID+"/steer", nil, http.StatusBadRequest)
	message, _ := body["error"].(string)
	if !strings.Contains(message, "HTTP followup delete unavailable") || body["steered"] != nil || body["turn"] != nil {
		t.Fatalf("storage failure was hidden or misclassified: %+v", body)
	}
	rows, err := h.app.Store.ListFollowups(id)
	if err != nil || len(rows) != 1 || rows[0].ID != f.ID || rows[0].Text != f.Text {
		t.Fatalf("failed HTTP steering lost the row: %+v err=%v", rows, err)
	}
	turns, err := h.app.Store.ListTurns(id)
	if err != nil || len(turns) != 0 {
		t.Fatalf("failed HTTP steering started work: %+v err=%v", turns, err)
	}
}

func TestComposerDeliveryHTTPBindsTheSelectedRowToTheActiveTurn(t *testing.T) {
	t.Setenv("ZWAI_MOCK_WORKER_DELAY_MS", "2200")
	h := newHarness(t)
	id := h.newThread()
	path := "/api/threads/" + id
	started := h.json(http.MethodPost, path+"/deliver", map[string]any{"mode": "queue", "text": "Start the first turn"}, http.StatusAccepted)
	turn := started["turn"].(map[string]any)["id"].(string)
	first := h.json(http.MethodPost, path+"/deliver", map[string]any{"mode": "queue", "text": "Keep this in line"}, http.StatusAccepted)
	second := h.json(http.MethodPost, path+"/deliver", map[string]any{"mode": "queue", "text": "Deliver this row"}, http.StatusAccepted)
	firstID := first["followup"].(map[string]any)["id"].(string)
	secondID := second["followup"].(map[string]any)["id"].(string)
	rowPath := path + "/followups/" + secondID + "/steer"
	refused := h.json(http.MethodPost, rowPath, map[string]any{"mode": "interrupt", "expected_turn_id": "another-turn"}, http.StatusConflict)
	if refused["code"] != "delivery_turn_changed" {
		t.Fatalf("stale turn response: %+v", refused)
	}
	accepted := h.json(http.MethodPost, rowPath, map[string]any{"mode": "interrupt", "expected_turn_id": turn}, http.StatusAccepted)
	if accepted["steered"] != true {
		t.Fatalf("wrong selection receipt: %+v", accepted)
	}
	queue, err := h.app.Store.ListFollowups(id)
	if err != nil || len(queue) != 1 || queue[0].ID != firstID {
		t.Fatalf("wrong queue after inserting second row: %+v %v", queue, err)
	}
}
