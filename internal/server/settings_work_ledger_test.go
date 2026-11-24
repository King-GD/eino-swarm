package server_test

import (
	"net/http"
	"testing"
)

func TestWorkLedgerSettingsOlderPutKeepsChoice(t *testing.T) {
	h := newHarness(t)
	swarm := func(method string, body map[string]any) map[string]any {
		t.Helper()
		out := h.json(method, "/api/settings", body, http.StatusOK)
		return out["settings"].(map[string]any)["swarm"].(map[string]any)
	}
	before := swarm(http.MethodGet, nil)
	if before["work_ledger"] != true {
		t.Fatalf("work records must default on: %v", before)
	}
	before["work_ledger"] = false
	if off := swarm(http.MethodPut, map[string]any{"swarm": before}); off["work_ledger"] != false {
		t.Fatalf("explicit opt out was ignored: %v", off)
	}
	delete(before, "work_ledger")
	if oldClient := swarm(http.MethodPut, map[string]any{"swarm": before}); oldClient["work_ledger"] != false {
		t.Fatalf("an older client reset the work record choice: %v", oldClient)
	}
}
