package remote

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

func appendPhoneEvent(t *testing.T, e *engine.Engine, threadID, turn, agent, kind, role, text string) int64 {
	t.Helper()
	row := &store.Event{ThreadID: threadID, TurnID: turn, AgentID: agent, Kind: kind, Role: role, Text: text}
	if err := e.Store().AppendEvent(row); err != nil {
		t.Fatal(err)
	}
	return row.Seq
}

func rosterIDs(t *testing.T, roster *[]EventView) []string {
	t.Helper()
	if roster == nil {
		t.Fatal("ready omitted the running-worker roster")
	}
	ids := make([]string, 0, len(*roster))
	for _, ev := range *roster {
		if ev.Kind != "spawned" || ev.Text != "" || ev.Seq <= 0 {
			t.Fatalf("roster row %+v", ev)
		}
		ids = append(ids, ev.AgentID)
	}
	return ids
}

func TestRunningWorkersOutsideThePhonePageStayOnTheReadyRoster(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("background workers", "", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := "launch-instruction-must-not-leave-the-host"
	add := func(turn, agent, kind, role, text string) int64 {
		return appendPhoneEvent(t, e, th.ID, turn, agent, kind, role, text)
	}
	add("old", "manager", "user_message", "", "start the sweep")
	add("old", "stopped-worker", "spawned", "stopped", secret)
	add("old", "manager", "cleanup", "", "stop")
	scanners := make([]int64, 4)
	for i, id := range []string{"scanner-1", "scanner-2", "scanner-3", "scanner-4"} {
		scanners[i] = add("old", id, "spawned", "scanner", secret)
	}
	add("old", "scanner-1", "finished", "scanner", "first pass ended")
	scanners[0] = add("old", "scanner-1", "spawned", "scanner", secret+" again")
	add("old", "done-worker", "spawned", "done", secret)
	add("old", "done-worker", "finished", "done", "finished")
	add("old", "manager", "error", "", "scheduled check failed")
	add("old", "manager", "agent_message", "", "old report")
	add("old", "manager", "done", "", "")
	prompt := add("new", "manager", "user_message", "", "check again")
	visible := add("new", "visible-worker", "spawned", "reader", secret)
	report := add("new", "manager", "agent_message", "", "latest report")
	add("new", "manager", "done", "", "")

	pump, log := testPump(t, e)
	raw, _ := json.Marshal(Request{
		V: ProtocolV, ID: "open", Op: OpWatch, ThreadID: th.ID,
		IncludeDetail: true, CompactHistory: true,
	})
	pump.Dispatch(raw)
	ready := log.waitID(t, "open", 2*time.Second)
	body, err := json.Marshal(ready)
	if err != nil || len(body) > plaintextBudget() || strings.Contains(string(body), secret) {
		t.Fatalf("ready leaked or exceeded the frame: bytes=%d err=%v", len(body), err)
	}
	if !ready.OK || ready.Op != OpReady || !ready.More || ready.HistoryBefore != prompt || ready.Seq < report {
		t.Fatalf("ready boundary %+v", ready)
	}
	got := map[int64]EventView{}
	for _, ev := range ready.Events {
		got[ev.Seq] = ev
		for _, spawn := range scanners {
			if ev.Seq == spawn {
				t.Fatalf("earlier spawn %d is in the first page", spawn)
			}
		}
	}
	if got[visible].Kind != "spawned" || got[visible].Text != "" || got[report].Text != "latest report" {
		t.Fatalf("latest page %+v", got)
	}
	// scanner-1 was finished and spawned again, so its current run is newest.
	if strings.Join(rosterIDs(t, ready.Roster), ",") != "scanner-2,scanner-3,scanner-4,scanner-1" {
		t.Fatalf("roster %v", ready.Roster)
	}
	for _, ev := range *ready.Roster {
		if ev.Role != "scanner" {
			t.Fatalf("role %+v", ev)
		}
	}

	raw, _ = json.Marshal(Request{V: ProtocolV, ID: "since", Op: OpWatch, ThreadID: th.ID, Since: prompt})
	pump.Dispatch(raw)
	replay := log.waitID(t, "since", 2*time.Second)
	if !replay.OK || replay.More || strings.Join(rosterIDs(t, replay.Roster), ",") != "scanner-2,scanner-3,scanner-4,scanner-1" {
		t.Fatalf("replay ready %+v", replay)
	}
	for _, ev := range replay.Events {
		if ev.Seq <= scanners[0] {
			t.Fatalf("replay included an earlier spawn %+v", ev)
		}
	}

	page := Handle(e, config.RemoteConfig{}, Request{Op: OpLog, ThreadID: th.ID, Before: prompt, CompactHistory: true}, "relay", "sess")
	if !page.OK || page.Roster != nil {
		t.Fatalf("older log page carried a roster: %+v", page.Roster)
	}
}

func TestPhoneRosterKeepsTheWorkersThePhoneWouldStillShowRunning(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("roster rules", "", "")
	if err != nil {
		t.Fatal(err)
	}
	add := func(agent, kind string) int64 {
		return appendPhoneEvent(t, e, th.ID, "turn", agent, kind, "role", "body")
	}
	first := add("again", "spawned")
	add("again", "finished")
	add("manager", "spawned")
	add("", "spawned")
	add("manager", "finished")
	add("", "finished")
	add("gone", "spawned")
	add("manager", "cleanup")
	held := add("held", "spawned")
	add("manager", "error")
	latest := add("again", "spawned")
	inPage := add("visible", "spawned")
	if first >= latest {
		t.Fatal("respawn did not get a later seq")
	}
	resp := attachRunningRoster(e.Store(), Response{
		OK: true, ThreadID: th.ID,
		Events: []EventView{{Seq: inPage, Kind: "spawned", AgentID: "visible", Text: ""}},
	}, config.RemoteConfig{})
	if strings.Join(rosterIDs(t, resp.Roster), ",") != "held,again" {
		t.Fatalf("roster %+v", resp.Roster)
	}
	if (*resp.Roster)[0].Seq != held || (*resp.Roster)[1].Seq != latest {
		t.Fatalf("order %+v", resp.Roster)
	}
	quiet, err := e.CreateThread("quiet", "", "")
	if err != nil {
		t.Fatal(err)
	}
	appendPhoneEvent(t, e, quiet.ID, "turn", "manager", "agent_message", "", "only a report")
	none := attachRunningRoster(e.Store(), Response{OK: true, ThreadID: quiet.ID}, config.RemoteConfig{})
	raw, err := json.Marshal(none)
	if err != nil || none.Roster == nil || len(*none.Roster) != 0 || !strings.Contains(string(raw), `"roster":[]`) {
		t.Fatalf("an idle conversation still needs an explicit empty roster: %s %+v", raw, none.Roster)
	}
}

func TestPhoneRosterDropsATimedOutWorkerAndKeepsItsPeer(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("watchdog", "", "")
	if err != nil {
		t.Fatal(err)
	}
	add := func(agent, kind, text string) int64 {
		return appendPhoneEvent(t, e, th.ID, "turn", agent, kind, "role", text)
	}
	peer := add("peer", "spawned", "body")
	add("victim", "spawned", "body")
	add("victim", "finished", "swarm: agent victim exceeded timeout 1s (1 consecutive watchdog failures): context deadline exceeded")
	add("manager", "error", "scheduled check failed")
	add("manager", "workers_retained", `{"worker_ids":["victim","peer"]}`)
	resp := attachRunningRoster(e.Store(), Response{OK: true, ThreadID: th.ID}, config.RemoteConfig{})
	if strings.Join(rosterIDs(t, resp.Roster), ",") != "peer" || (*resp.Roster)[0].Seq != peer {
		t.Fatalf("timeout did not leave the peer: %+v", resp.Roster)
	}
	again := add("victim", "spawned", "body")
	resp = attachRunningRoster(e.Store(), Response{OK: true, ThreadID: th.ID}, config.RemoteConfig{})
	if strings.Join(rosterIDs(t, resp.Roster), ",") != "peer,victim" || (*resp.Roster)[1].Seq != again {
		t.Fatalf("respawn after timeout: %+v", resp.Roster)
	}
	add("manager", "cleanup", "")
	resp = attachRunningRoster(e.Store(), Response{OK: true, ThreadID: th.ID}, config.RemoteConfig{})
	if resp.Roster == nil || len(*resp.Roster) != 0 {
		t.Fatalf("cleanup revived a worker: %+v", resp.Roster)
	}
}

func TestPhoneRosterShrinksToTheFrameInsteadOfFailingTheOpen(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("roster budget", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var oldest, newest int64
	for i := 0; i < 400; i++ {
		seq := appendPhoneEvent(t, e, th.ID, "turn", "worker-"+strconv.Itoa(i), "spawned", "worker", "secret")
		if i == 0 {
			oldest = seq
		}
		newest = seq
	}
	resp := attachRunningRoster(e.Store(), Response{
		OK: true, ThreadID: th.ID,
		Events: []EventView{{Seq: newest + 1, Kind: "agent_message", Text: strings.Repeat("x", 50000)}},
	}, config.RemoteConfig{})
	raw, err := json.Marshal(resp)
	if err != nil || resp.Roster == nil {
		t.Fatalf("roster %v err=%v", resp.Roster, err)
	}
	ids := rosterIDs(t, resp.Roster)
	if len(raw) > plaintextBudget() || len(ids) == 0 || len(ids) >= 400 ||
		ids[0] == "worker-0" || ids[len(ids)-1] != "worker-399" || strings.Contains(string(raw), "secret") {
		t.Fatalf("bytes=%d kept=%d oldest=%d", len(raw), len(ids), oldest)
	}
	if resp.Events[0].Text != strings.Repeat("x", 50000) {
		t.Fatal("trimming the roster shortened the conversation page")
	}
}

func TestPhoneRosterReadFailureLeavesTheSnapshot(t *testing.T) {
	e := testEngine(t)
	th, err := e.CreateThread("roster closed", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store().Close(); err != nil {
		t.Fatal(err)
	}
	resp := Response{OK: true, ThreadID: th.ID, Events: []EventView{{Seq: 1, Kind: "agent_message", Text: "kept"}}}
	got := attachRunningRoster(e.Store(), resp, config.RemoteConfig{})
	if !got.OK || got.Roster != nil || len(got.Events) != 1 || got.Events[0].Text != "kept" {
		t.Fatalf("%+v", got)
	}
}

func TestPhoneRosterIgnoresAReplyThatIsNotASnapshot(t *testing.T) {
	if got := attachRunningRoster(nil, Response{OK: false, ThreadID: "t"}, config.RemoteConfig{}); got.OK || got.Roster != nil {
		t.Fatalf("%+v", got)
	}
	if got := attachRunningRoster(nil, Response{OK: true}, config.RemoteConfig{}); got.Roster != nil {
		t.Fatalf("%+v", got)
	}
	kept := Response{OK: true, Events: []EventView{{Text: "kept"}}}
	if got := fitRoster(kept); got.Roster != nil || len(got.Events) != 1 || got.Events[0].Text != "kept" {
		t.Fatalf("%+v", got)
	}
}
