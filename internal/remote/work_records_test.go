package remote

import (
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func workPhoneTurn(t *testing.T, e *engine.Engine, projectID string) store.Turn {
	t.Helper()
	thread, err := e.CreateThread("Work records fixture", "", projectID)
	if err != nil {
		t.Fatal(err)
	}
	turn := store.Turn{ThreadID: thread.ID, ProjectID: projectID}
	if err := e.Store().CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestPairedPhoneWorkRecordsRespectSavedDependenciesAndScope(t *testing.T) {
	e := testEngine(t)
	project, err := e.CreateProject("Project", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.CreateProject("Other project", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	turn, peer, foreign := workPhoneTurn(t, e, project.ID), workPhoneTurn(t, e, project.ID), workPhoneTurn(t, e, other.ID)
	save := func(owner store.Turn, scope, kind string, deps ...string) store.NuwaNode {
		t.Helper()
		node := store.NuwaNode{
			ThreadID: owner.ThreadID, TurnID: owner.ID, Scope: scope, AgentID: "manager",
			Kind: kind, Title: kind, DependsOn: deps,
		}
		if scope == store.WorkScopeProject {
			node.ProjectID = owner.ProjectID
		}
		if kind == "artifact" {
			node.ArtifactPath = "results/report.md"
		}
		if err := e.Store().UpsertNuwaNode(t.Context(), &node, 0); err != nil {
			t.Fatal(err)
		}
		return node
	}
	task := save(peer, store.WorkScopeProject, "task")
	fact := save(peer, store.WorkScopeProject, "fact", task.ID)
	result := save(turn, store.WorkScopeThread, "artifact", fact.ID)
	save(peer, store.WorkScopeThread, "fact")
	save(foreign, store.WorkScopeProject, "fact")

	relay := newFollowupRelay(t, e)
	meta := relay.rpc(t, Request{ID: "meta", Op: OpMeta})
	if !meta.OK || !meta.WorkRecordsAvailable || meta.DataGeneration == nil {
		t.Fatalf("paired host did not advertise work records: %+v", meta)
	}
	reply := relay.rpc(t, Request{ID: "view", Op: OpThreadWork, ThreadID: turn.ThreadID, ProjectID: other.ID})
	view := reply.WorkSnapshot
	if !reply.OK || view == nil || view.ThreadID != turn.ThreadID || view.ProjectID != project.ID ||
		reply.DataGeneration == nil || *reply.DataGeneration != *meta.DataGeneration || len(view.Nodes) != 3 {
		t.Fatalf("incorrect paired projection: %+v", reply)
	}
	byID := make(map[string]store.NuwaNode)
	for _, node := range view.Nodes {
		byID[node.ID] = node
	}
	if record := byID[result.ID]; record.Scope != store.WorkScopeThread || record.IncompleteBasis ||
		len(record.DependsOn) != 1 || record.DependsOn[0] != fact.ID || byID[task.ID].Scope != store.WorkScopeProject {
		t.Fatalf("dependency chain changed in transport: %+v", byID)
	}
	for _, test := range []struct{ thread, code string }{{"", "bad_request"}, {"missing", "not_found"}} {
		reply := relay.rpc(t, Request{ID: test.code, Op: OpThreadWork, ThreadID: test.thread})
		if reply.OK || reply.Code != test.code || reply.WorkSnapshot != nil {
			t.Fatalf("incorrect refusal for %s: %+v", test.thread, reply)
		}
	}
	if err := e.UpdateConfig(func(cfg *config.Config) error { cfg.Swarm.WorkLedger = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if meta := relay.rpc(t, Request{ID: "off-meta", Op: OpMeta}); !meta.OK || meta.WorkRecordsAvailable {
		t.Fatalf("disabled records were advertised: %+v", meta)
	}
	if reply := relay.rpc(t, Request{ID: "off-read", Op: OpThreadWork, ThreadID: turn.ThreadID}); reply.OK || reply.Code != "work_disabled" || reply.WorkSnapshot != nil {
		t.Fatalf("disabled records left the PC: %+v", reply)
	}
}

func TestPairedPhoneWorkRecordsRefuseOversizeWithoutBreakingLink(t *testing.T) {
	e := testEngine(t)
	turn := workPhoneTurn(t, e, "")
	node := store.NuwaNode{ThreadID: turn.ThreadID, TurnID: turn.ID, AgentID: "manager", Scope: store.WorkScopeThread,
		Kind: "fact", Title: "Large report", Detail: strings.Repeat("a", MaxPushPayload)}
	if err := e.Store().UpsertNuwaNode(t.Context(), &node, 0); err != nil {
		t.Fatal(err)
	}
	relay := newFollowupRelay(t, e)
	reply := relay.rpc(t, Request{ID: "oversize", Op: OpThreadWork, ThreadID: turn.ThreadID})
	if reply.OK || reply.Code != "work_too_large" || reply.WorkSnapshot != nil {
		t.Fatalf("large graph was sent: %+v", reply)
	}
	reply = relay.rpc(t, Request{ID: "still-live", Op: OpMeta})
	if !reply.OK {
		t.Fatalf("frame refusal closed the paired link: %+v", reply)
	}
}
