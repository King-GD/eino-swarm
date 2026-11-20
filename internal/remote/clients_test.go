package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestPhoneNeverReceivesLocalClientTasks(t *testing.T) {
	e := testEngine(t)
	root := t.TempDir()
	file := filepath.Join(root, "projects", "work", "local.jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"type":"user","message":{"content":"private local task"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Deliberately bypass settings normalization to exercise the wire boundary.
	e.Config().Clients.Enabled = true
	e.Config().Clients.ClaudeDir = root
	if _, err := e.CreateThread("", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := config.RemoteConfig{ThreadLimit: 5}
	for _, tc := range []struct {
		name    string
		req     Request
		code    string
		catalog string
	}{
		{name: "inbox", req: Request{Op: OpList}},
		{name: "inbox section", req: Request{Op: OpList, Group: "recent"}},
		{name: "inbox pagination", req: Request{Op: OpMore}},
		{name: "old phone poll", req: Request{Op: OpClients}, catalog: `{"enabled":false}`},
		{name: "old phone pagination", req: Request{Op: OpClients, Group: "claude", Before: 1}, catalog: `{"enabled":false}`},
		{name: "known task", req: Request{Op: OpClientRead, TaskID: "claude:local"}, code: "not_found"},
		{name: "invalid page", req: Request{Op: OpClientRead, TaskID: "claude:local", Before: -1}, code: "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := Handle(e, cfg, tc.req, "relay", "s")
			if resp.OK != (tc.code == "") || resp.Code != tc.code {
				t.Errorf("unexpected result: OK=%v code=%q", resp.OK, resp.Code)
			}
			if tc.name == "inbox" && (len(resp.Threads) != 1 || len(resp.Groups) == 0) {
				t.Errorf("inbox lost its conversation data: %+v", resp)
			}
			raw, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["clients"]) != tc.catalog || fields["client_view"] != nil {
				t.Errorf("unexpected local task fields: %s", raw)
			}
		})
	}
}
