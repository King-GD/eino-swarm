package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func addWorkThread(t *testing.T, s *Store, projectID string) (Thread, Turn) {
	t.Helper()
	thread := Thread{Title: "record owner", ProjectID: projectID}
	if err := s.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	turn := Turn{ThreadID: thread.ID, ProjectID: projectID, WorkLedger: true}
	if err := s.CreateTurn(&turn); err != nil {
		t.Fatal(err)
	}
	return thread, turn
}

func newWorkNode(turn Turn, kind, scope string) NuwaNode {
	node := NuwaNode{ThreadID: turn.ThreadID, TurnID: turn.ID, AgentID: "writer", Generation: 2, Kind: kind, Title: "reviewed work", Scope: scope}
	if scope == WorkScopeProject {
		node.ProjectID = turn.ProjectID
	}
	return node
}

func saveTestWork(t *testing.T, s *Store, turn Turn, node *NuwaNode, expected int64) {
	t.Helper()
	if err := s.UpsertNuwaNode(t.Context(), node, expected, NuwaWriteReceipt{CallerProjectID: &turn.ProjectID}); err != nil {
		t.Fatal(err)
	}
}

func mustWorkSnapshot(t *testing.T, s *Store, threadID string) WorkSnapshot {
	t.Helper()
	snapshot, err := s.WorkSnapshot(t.Context(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func findTestWork(t *testing.T, nodes []NuwaNode, id string) NuwaNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("work record %s absent in %+v", id, nodes)
	return NuwaNode{}
}

func TestWorkLedgerScopesVisibilityEvidenceAndVerification(t *testing.T) {
	f := newNuwaFixture(t)
	s := f.s
	source, sourceTurn := addWorkThread(t, s, f.project.ID)
	peer, peerTurn := addWorkThread(t, s, f.project.ID)
	loose, looseTurn := addWorkThread(t, s, "")
	marked := Event{ThreadID: source.ID, TurnID: sourceTurn.ID, Kind: "tool_result", WorkRecordPayload: true}
	if err := s.AppendEvent(&marked); err != nil {
		t.Fatal(err)
	}
	private := newWorkNode(sourceTurn, "fact", WorkScopeThread)
	private.Evidence = []NuwaEvidence{{ThreadID: source.ID, TurnID: sourceTurn.ID, Seq: marked.Seq}}
	saveTestWork(t, s, sourceTurn, &private, 0)
	shared := newWorkNode(peerTurn, "task", WorkScopeProject)
	saveTestWork(t, s, peerTurn, &shared, 0)
	standalone := newWorkNode(looseTurn, "fact", WorkScopeThread)
	saveTestWork(t, s, looseTurn, &standalone, 0)

	own, err := s.WorkSnapshotForTurn(t.Context(), f.project.ID, source.ID, sourceTurn.ID)
	if err != nil || len(own.Nodes) != 2 || own.ThreadRevision != 1 || own.ProjectRevision != 1 {
		t.Fatalf("source saw wrong visible set: %+v, %v", own, err)
	}
	for threadID, want := range map[string]string{peer.ID: shared.ID, loose.ID: standalone.ID} {
		view := mustWorkSnapshot(t, s, threadID)
		if len(view.Nodes) != 1 || view.Nodes[0].ID != want {
			t.Fatalf("scope leak for %s: %+v", threadID, view)
		}
	}
	_, projectNodes, err := s.NuwaSnapshot(t.Context(), f.project.ID)
	if err != nil || len(projectNodes) != 1 || projectNodes[0].ID != shared.ID {
		t.Fatalf("project-only scope leaked: %+v, %v", projectNodes, err)
	}
	foreign := newWorkNode(peerTurn, "fact", WorkScopeThread)
	foreign.Evidence = private.Evidence
	if err := s.UpsertNuwaNode(t.Context(), &foreign, 0); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("foreign private evidence: %v", err)
	}
	foreign.Evidence = nil
	foreign.DependsOn = []string{private.ID}
	if err := s.UpsertNuwaNode(t.Context(), &foreign, 0); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("foreign private prerequisite: %v", err)
	}
	publish := newWorkNode(sourceTurn, "fact", WorkScopeProject)
	publish.Evidence = private.Evidence
	if err := s.UpsertNuwaNode(t.Context(), &publish, 0); !errors.Is(err, ErrInvalidNuwaNode) || !strings.Contains(err.Error(), "original event") {
		t.Fatalf("project work accepted private tool payload: %v", err)
	}
	in := NuwaVerifyAdmission{ProjectID: f.project.ID, ThreadID: peer.ID, TurnID: peerTurn.ID, AgentID: "writer", CallID: "verify", Intent: "verify", TargetID: private.ID}
	if err := s.AdmitNuwaVerification(t.Context(), in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peer verify reached another conversation's private target: %v", err)
	}
	in.ThreadID, in.TurnID = source.ID, sourceTurn.ID
	if err := s.AdmitNuwaVerification(t.Context(), in); err != nil {
		t.Fatal(err)
	}

	_, err = s.PromoteNuwaNodes(t.Context(), NuwaPromoteInput{ThreadID: source.ID, IDs: []string{private.ID}, Expected: &own.WorkRevision})
	if !errors.Is(err, ErrInvalidNuwaNode) || !strings.Contains(err.Error(), "original event") {
		t.Fatalf("promotion accepted tagged evidence: %v", err)
	}
	looseView := mustWorkSnapshot(t, s, loose.ID)
	_, err = s.PromoteNuwaNodes(t.Context(), NuwaPromoteInput{ThreadID: loose.ID, IDs: []string{standalone.ID}, Expected: &looseView.WorkRevision})
	if !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("standalone promoted into a project: %v", err)
	}
}

func TestWorkLedgerPromotesClosureAndPreservesDependencyReview(t *testing.T) {
	f := newNuwaFixture(t)
	s := f.s
	thread, turn := addWorkThread(t, s, f.project.ID)
	peer, _ := addWorkThread(t, s, f.project.ID)
	source := newWorkNode(turn, "fact", WorkScopeThread)
	saveTestWork(t, s, turn, &source, 0)
	stale := newWorkNode(turn, "task", WorkScopeThread)
	stale.DependsOn = []string{source.ID}
	saveTestWork(t, s, turn, &stale, 0)
	source.Status = "retracted"
	saveTestWork(t, s, turn, &source, 1)
	source.Status = "reported"
	saveTestWork(t, s, turn, &source, 2)
	fresh := newWorkNode(turn, "task", WorkScopeThread)
	fresh.DependsOn = []string{source.ID}
	saveTestWork(t, s, turn, &fresh, 0)
	root := newWorkNode(turn, "fact", WorkScopeThread)
	root.DependsOn = []string{stale.ID, fresh.ID}
	saveTestWork(t, s, turn, &root, 0)

	before := mustWorkSnapshot(t, s, thread.ID)
	promotion, err := s.PromoteNuwaNodes(t.Context(), NuwaPromoteInput{ThreadID: thread.ID, IDs: []string{root.ID}, Expected: &before.WorkRevision})
	if err != nil || len(promotion.Nodes) != 4 || promotion.ThreadRevision != before.ThreadRevision+1 || promotion.ProjectRevision != before.ProjectRevision+4 {
		t.Fatalf("incomplete promotion: %+v %v", promotion, err)
	}
	peerView := mustWorkSnapshot(t, s, peer.ID)
	for _, node := range peerView.Nodes {
		if node.Scope != WorkScopeProject || node.ProjectID != f.project.ID || node.UpdatedBy != MessageSourceHuman ||
			node.ThreadID != thread.ID || node.TurnID != turn.ID || node.AgentID != "writer" || node.UpdatedTurnID != "" {
			t.Fatalf("promotion changed origin or misattributed human: %+v", node)
		}
	}
	fresh = findTestWork(t, peerView.Nodes, fresh.ID)
	stale = findTestWork(t, peerView.Nodes, stale.ID)
	if len(fresh.StaleDependencies) != 0 || !slices.Equal(stale.StaleDependencies, []string{source.ID}) || fresh.DependencyVersions[source.ID] != 4 || stale.DependencyVersions[source.ID] != 1 {
		t.Fatalf("promotion changed reviewed basis: fresh=%+v stale=%+v", fresh, stale)
	}
	var rows int64
	if err := s.DB().Model(&NuwaRevision{}).Where("node_id = ?", source.ID).Count(&rows).Error; err != nil || rows != 4 {
		t.Fatalf("expected every version across both scopes, got %d %v", rows, err)
	}
	_, err = s.PromoteNuwaNodes(t.Context(), NuwaPromoteInput{ThreadID: thread.ID, IDs: []string{root.ID}, Expected: &before.WorkRevision})
	if !errors.Is(err, ErrNuwaConflict) {
		t.Fatalf("stale human view accepted: %v", err)
	}
}

func TestWorkLedgerProjectCreateAndAgentPromotionRollbackAndReplay(t *testing.T) {
	f := newNuwaFixture(t)
	s := f.s
	thread, turn := addWorkThread(t, s, f.project.ID)
	source := newWorkNode(turn, "fact", WorkScopeThread)
	saveTestWork(t, s, turn, &source, 0)
	root := newWorkNode(turn, "task", WorkScopeThread)
	root.DependsOn = []string{source.ID}
	saveTestWork(t, s, turn, &root, 0)
	before := mustWorkSnapshot(t, s, thread.ID)

	if err := s.DB().Exec(`CREATE TRIGGER work_promotion_fault BEFORE UPDATE ON nuwa_nodes
		WHEN NEW.scope = 'project' AND NEW.kind = 'task' BEGIN SELECT RAISE(IGNORE); END`).Error; err != nil {
		t.Fatal(err)
	}
	creation := newWorkNode(turn, "fact", WorkScopeProject)
	creation.DependsOn = []string{root.ID}
	if err := s.UpsertNuwaNode(t.Context(), &creation, 0); !errors.Is(err, ErrNuwaConflict) {
		t.Fatalf("failed closure CAS accepted project creation: %v", err)
	}
	if got := mustWorkSnapshot(t, s, thread.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed closure leaked a revision or record: %+v", got)
	}
	if err := s.DB().Exec("DROP TRIGGER work_promotion_fault").Error; err != nil {
		t.Fatal(err)
	}

	saveTestWork(t, s, turn, &creation, 0)
	view := mustWorkSnapshot(t, s, thread.ID)
	if len(view.Nodes) != 3 || view.ThreadRevision != 3 || view.ProjectRevision != 3 || creation.DependencyVersions[root.ID] != 2 {
		t.Fatalf("project create did not publish entire closure: %+v", view)
	}
	latest := newWorkNode(turn, "fact", WorkScopeThread)
	receipt := NuwaWriteReceipt{Key: "new-private-record", Hash: "input", CallerProjectID: &turn.ProjectID}
	if err := s.UpsertNuwaNode(t.Context(), &latest, 0, receipt); err != nil {
		t.Fatal(err)
	}
	in := NuwaPromoteInput{ThreadID: thread.ID, ProjectID: turn.ProjectID, TurnID: turn.ID, AgentID: "writer", Generation: 2,
		IDs: []string{latest.ID}, Version: latest.Version, Receipt: NuwaWriteReceipt{Key: "promote-private-record", Hash: "promote input"}}
	first, err := s.PromoteNuwaNodes(t.Context(), in)
	if err != nil || len(first.Nodes) != 1 || first.Nodes[0].Version != 2 {
		t.Fatalf("agent promotion %+v %v", first, err)
	}
	replay, err := s.PromoteNuwaNodes(t.Context(), in)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("agent promotion replay %+v %v", replay, err)
	}
	replayedCreate := newWorkNode(turn, "fact", WorkScopeThread)
	if err := s.UpsertNuwaNode(t.Context(), &replayedCreate, 0, receipt); err != nil || replayedCreate.Scope != WorkScopeThread || replayedCreate.Version != 1 {
		t.Fatalf("private receipt returned the later version: %+v %v", replayedCreate, err)
	}
	in.Receipt.Hash = "different content"
	if _, err := s.PromoteNuwaNodes(t.Context(), in); !errors.Is(err, ErrNuwaConflict) {
		t.Fatalf("receipt reuse accepted different promotion: %v", err)
	}
}

func TestWorkLedgerMoveRewindDeleteAndRosterShareRevisionOwnership(t *testing.T) {
	f := newNuwaFixture(t)
	s := f.s
	thread, turn := addWorkThread(t, s, f.project.ID)
	event := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "spawned", AgentID: "worker"}
	if err := s.AppendEvent(&event); err != nil {
		t.Fatal(err)
	}
	private := newWorkNode(turn, "fact", WorkScopeThread)
	private.Evidence = []NuwaEvidence{{ThreadID: thread.ID, TurnID: turn.ID, Seq: event.Seq}}
	saveTestWork(t, s, turn, &private, 0)
	shared := newWorkNode(turn, "fact", WorkScopeProject)
	saveTestWork(t, s, turn, &shared, 0)
	before, events, err := s.WorkSnapshotWithEvents(t.Context(), thread.ID, []string{"spawned"})
	if err != nil || len(events) != 1 || before.ThreadRevision != 2 || before.ProjectRevision != 2 {
		t.Fatalf("roster and revisions did not come from durable state: %+v %+v %v", before, events, err)
	}
	failed := failNuwaOperation(t, s, "create", "nuwa_revisions", 1)
	checkpoint := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "engine_restart_checkpoint"}
	if err := s.AppendEvent(&checkpoint); err == nil || !*failed {
		t.Fatalf("failed roster revision accepted its event: %v", err)
	}
	if _, err := s.GetEvent(thread.ID, checkpoint.Seq); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed roster event committed: %v", err)
	}
	if got := mustWorkSnapshot(t, s, thread.ID); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed roster event advanced view: %+v", got)
	}
	newProject := Project{Name: "destination"}
	if err := s.CreateProject(&newProject); err != nil {
		t.Fatal(err)
	}
	if err := s.SetThreadProject(thread.ID, newProject.ID); err != nil {
		t.Fatal(err)
	}
	moved := mustWorkSnapshot(t, s, thread.ID)
	if moved.ProjectID != newProject.ID || len(moved.Nodes) != 1 || moved.Nodes[0].ID != private.ID ||
		moved.ThreadRevision != before.ThreadRevision+1 || len(moved.Nodes[0].UnavailableEvidence) != 0 {
		t.Fatalf("move lost private nodes/evidence or moved a project node: %+v", moved)
	}
	in := NuwaPromoteInput{ThreadID: thread.ID, IDs: []string{private.ID}, Expected: &before.WorkRevision}
	if _, err := s.PromoteNuwaNodes(t.Context(), in); !errors.Is(err, ErrNuwaConflict) {
		t.Fatalf("old destination accepted moved scope: %v", err)
	}
	if _, err := s.WorkSnapshotForTurn(t.Context(), f.project.ID, thread.ID, turn.ID); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("old caller retained visibility after move: %v", err)
	}
	if err := s.TruncateFromEventSeq(thread.ID, event.Seq); err != nil {
		t.Fatal(err)
	}
	rewound := mustWorkSnapshot(t, s, thread.ID)
	if rewound.ThreadRevision != moved.ThreadRevision+1 || len(rewound.Nodes) != 1 ||
		len(rewound.Nodes[0].UnavailableEvidence) != 1 || rewound.Nodes[0].Version != 1 {
		t.Fatalf("rewind erased work or left the evidence available: %+v", rewound)
	}
	projectBefore, _, err := s.NuwaSnapshot(t.Context(), f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProject(newProject.ID); err != nil {
		t.Fatal(err)
	}
	projectAfter, remaining, err := s.NuwaSnapshot(t.Context(), f.project.ID)
	if err != nil || projectAfter != projectBefore+1 || len(remaining) != 0 {
		t.Fatalf("project delete missed member's historic published work: %d %d %+v %v", projectBefore, projectAfter, remaining, err)
	}
	var audit int64
	if err := s.DB().Model(&NuwaRevision{}).Where("node_id IN ? OR thread_id = ?", []string{shared.ID, private.ID}, thread.ID).Count(&audit).Error; err != nil || audit != 0 {
		t.Fatalf("orphaned owned history: %d %v", audit, err)
	}
}

func TestWorkLedgerMigratesLegacyProjectRowsAndRevisionIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "work-migration.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Project{Name: "legacy work"}
	if err := s.CreateProject(&p); err != nil {
		t.Fatal(err)
	}
	thread, turn := addWorkThread(t, s, p.ID)
	legacy := newWorkNode(turn, "fact", WorkScopeProject)
	receipt := NuwaWriteReceipt{Key: "legacy-request", Hash: "legacy-arguments"}
	if err := s.UpsertNuwaNode(t.Context(), &legacy, 0, receipt); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX idx_nuwa_scoped_revision", "DROP INDEX idx_nuwa_request_key",
		"ALTER TABLE nuwa_nodes DROP COLUMN scope", "ALTER TABLE nuwa_revisions DROP COLUMN thread_id",
		"CREATE UNIQUE INDEX idx_nuwa_revision ON nuwa_revisions(project_id, revision)",
		"CREATE UNIQUE INDEX idx_nuwa_request ON nuwa_revisions(project_id, request_key)",
		"UPDATE nuwa_revisions SET payload = json_remove(payload, '$.scope')",
	} {
		if err := s.DB().Exec(statement).Error; err != nil {
			t.Fatalf("legacy table setup %s: %v", statement, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.DB().Migrator().HasIndex(&NuwaRevision{}, "idx_nuwa_revision") ||
		s.DB().Migrator().HasIndex(&NuwaRevision{}, "idx_nuwa_request") {
		t.Fatal("legacy revision indexes still constrain per-thread history")
	}
	legacyRetry := newWorkNode(turn, "fact", WorkScopeProject)
	if err := s.UpsertNuwaNode(t.Context(), &legacyRetry, 0, receipt); err != nil ||
		legacyRetry.ID != legacy.ID || legacyRetry.Scope != WorkScopeProject {
		t.Fatalf("legacy receipt did not preserve shared identity: %+v %v", legacyRetry, err)
	}
	other, otherTurn := addWorkThread(t, s, p.ID)
	for _, owner := range []Turn{turn, otherTurn} {
		node := newWorkNode(owner, "fact", WorkScopeThread)
		saveTestWork(t, s, owner, &node, 0)
	}
	for _, owner := range []Thread{thread, other} {
		view := mustWorkSnapshot(t, s, owner.ID)
		if view.ThreadRevision != 1 || view.ProjectRevision != 1 || len(view.Nodes) != 2 || view.Nodes[0].ID != legacy.ID {
			t.Fatalf("legacy migrated data/revisions are wrong: %+v", view)
		}
	}
}

func TestWorkLedgerConcurrentHumanPromotionHasOneCommittedWinner(t *testing.T) {
	f := newNuwaFixture(t)
	thread, turn := addWorkThread(t, f.s, f.project.ID)
	node := newWorkNode(turn, "fact", WorkScopeThread)
	saveTestWork(t, f.s, turn, &node, 0)
	before := mustWorkSnapshot(t, f.s, thread.ID)
	in := NuwaPromoteInput{ThreadID: thread.ID, IDs: []string{node.ID}, Expected: &before.WorkRevision}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := f.s.PromoteNuwaNodes(t.Context(), in)
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrNuwaConflict)) || (second == nil && errors.Is(first, ErrNuwaConflict))) {
		t.Fatalf("expected one committed promotion and one conflict, got %v / %v", first, second)
	}
	after := mustWorkSnapshot(t, f.s, thread.ID)
	if after.ThreadRevision != before.ThreadRevision+1 || after.ProjectRevision != before.ProjectRevision+1 ||
		len(after.Nodes) != 1 || after.Nodes[0].Version != 2 || after.Nodes[0].Scope != WorkScopeProject {
		t.Fatalf("concurrent click committed an extra or incomplete result: %+v", after)
	}
}

func TestWorkLedgerTaggedOwnReceiptAndFrozenPlanning(t *testing.T) {
	f := newNuwaFixture(t)
	thread, turn := addWorkThread(t, f.s, f.project.ID)
	for _, kind := range []string{"tool_call", "tool_result"} {
		if err := f.s.AppendEvent(&Event{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "writer",
			WorkerGeneration: 2, Kind: kind, ToolCallID: "private-receipt", WorkRecordPayload: true}); err != nil {
			t.Fatal(err)
		}
	}
	receipt := NuwaWriteReceipt{CallerProjectID: &turn.ProjectID, OwnToolCallIDs: []string{"private-receipt"}, CurrentToolCallID: "record"}
	projectNode := newWorkNode(turn, "fact", WorkScopeProject)
	if err := f.s.UpsertNuwaNode(t.Context(), &projectNode, 0, receipt); !errors.Is(err, errWorkPayloadEvidence) {
		t.Fatalf("project accepted an own-tool private receipt: %v", err)
	}
	private := newWorkNode(turn, "fact", WorkScopeThread)
	if err := f.s.UpsertNuwaNode(t.Context(), &private, 0, receipt); err != nil || len(private.Evidence) != 1 {
		t.Fatalf("same-thread receipt was unavailable: %+v %v", private, err)
	}
	if err := f.s.UpdateTurn(turn.ID, map[string]any{"work_ledger_read_only": true}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.UpsertNuwaNode(t.Context(), &private, 1, receipt); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("frozen planning turn wrote work: %v", err)
	}
	if snapshot, err := f.s.WorkSnapshotForTurn(t.Context(), f.project.ID, thread.ID, turn.ID); err != nil || len(snapshot.Nodes) != 1 {
		t.Fatalf("frozen planning turn lost read access: %+v %v", snapshot, err)
	}
}

func TestWorkLedgerIndependentRosterRevisionAndAtomicBatch(t *testing.T) {
	s := open(t)
	thread, turn := addWorkThread(t, s, "")
	if revision, err := s.WorkSnapshotRevision(t.Context(), thread.ID); err != nil ||
		revision != (WorkRevision{ThreadID: thread.ID}) {
		t.Fatalf("initial standalone revisions: %+v %v", revision, err)
	}
	if _, err := s.WorkSnapshotRevision(t.Context(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stream owner produced a revision: %v", err)
	}
	if _, err := s.WorkSnapshotForTurn(t.Context(), "", thread.ID, ""); !errors.Is(err, ErrInvalidNuwaNode) {
		t.Fatalf("agent read without an original turn: %v", err)
	}
	rows := []*Event{
		{ThreadID: thread.ID, TurnID: turn.ID, Kind: "tool_result"},
		{ThreadID: thread.ID, TurnID: turn.ID, AgentID: "worker", Kind: "finished"},
	}
	if err := s.AppendEventsAtomic(rows); err != nil {
		t.Fatal(err)
	}
	revision, err := s.WorkSnapshotRevision(t.Context(), thread.ID)
	if err != nil || revision.ThreadRevision != 1 || revision.ProjectRevision != 0 {
		t.Fatalf("worker update missed standalone SSE revision: %+v %v", revision, err)
	}
	injected := failNuwaOperation(t, s, "update", "threads", 1)
	marker := Event{ThreadID: thread.ID, TurnID: turn.ID, Kind: "restart_preparing"}
	if err := s.AppendEventsAtomic([]*Event{&marker}); err == nil || !*injected {
		t.Fatalf("failed thread clock accepted handoff event: %v", err)
	}
	if _, err := s.GetEvent(thread.ID, marker.Seq); !errors.Is(err, ErrNotFound) {
		t.Fatalf("handoff event survived failed revision transaction: %v", err)
	}
	if after, err := s.WorkSnapshotRevision(t.Context(), thread.ID); err != nil || after != revision {
		t.Fatalf("failed batch persisted a clock update: %+v %v", after, err)
	}
}

func TestWorkLedgerIncompletePromotionLeavesTheOriginalScope(t *testing.T) {
	f := newNuwaFixture(t)
	thread, turn := addWorkThread(t, f.s, f.project.ID)
	shared := newWorkNode(turn, "fact", WorkScopeProject)
	saveTestWork(t, f.s, turn, &shared, 0)
	private := newWorkNode(turn, "fact", WorkScopeThread)
	private.DependsOn = []string{shared.ID}
	saveTestWork(t, f.s, turn, &private, 0)
	destination := Project{Name: "new membership"}
	if err := f.s.CreateProject(&destination); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetThreadProject(thread.ID, destination.ID); err != nil {
		t.Fatal(err)
	}
	before := mustWorkSnapshot(t, f.s, thread.ID)
	for name, ids := range map[string][]string{
		"missing":   {private.ID},
		"duplicate": {private.ID, private.ID},
		"blank":     {""},
		"empty":     nil,
		"unknown":   {"unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			request := NuwaPromoteInput{ThreadID: thread.ID, IDs: ids, Expected: &before.WorkRevision}
			if _, err := f.s.PromoteNuwaNodes(t.Context(), request); !errors.Is(err, ErrInvalidNuwaNode) {
				t.Fatalf("unavailable selection accepted: %v", err)
			}
		})
	}
	if after := mustWorkSnapshot(t, f.s, thread.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused promotion changed the graph: %+v", after)
	}
}
