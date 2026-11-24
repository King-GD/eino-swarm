-------------------------- MODULE NuwaCollaboration --------------------------
(* A bounded collaboration registry and its project evidence stream. This is
   a protocol model, not a proof of Go atomicity. The real Registry/HTTP tests
   must establish that the code uses these same ownership boundaries. *)
EXTENDS Naturals, FiniteSets

CONSTANTS
    RebindOnRestore,
    CancelParentOnly,
    WriteAfterStop,
    CrossProjectEvidence,
    PublishBeforeCommit,
    RewindSkipsRevision,
    DeleteMovedSkipsRevision,
    PromoteWithoutClosure,
    PrivateLeak,
    PromoteIgnoresThreadRevision,
    PromoteIgnoresProjectRevision,
    PromoteIgnoresMove,
    PromoteIgnoresDataGeneration,
    PromotePartiallyCommits,
    PromotePublishesStaged,
    MaxWrites

Workers == {"parent", "child"}
Models == {"planner", "executor"}
Chosen == [w \in Workers |-> IF w = "parent" THEN "planner" ELSE "executor"]

VARIABLES phase, live, saved, binding, written, published, lateWrite,
          foreignEvidence, restored, stopped, sourceRequired, approved, branch, ledger, merge

registry == <<phase, live, saved, binding, written, published, lateWrite,
              foreignEvidence, restored, stopped, sourceRequired, approved>>
vars == <<registry, branch, ledger, merge>>

Threads == {"owner", "peer", "standalone"}
Nodes == {"root", "source", "standalone-record"}
NodeThread(node) == IF node = "standalone-record" THEN "standalone" ELSE "owner"
Dependencies(node) == IF node = "root" THEN {"source"} ELSE {}
ProjectThreads == Threads \ {"standalone"}
Scopes == {"none", "thread", "project"}
EmptyLedger == [scope |-> [node \in Nodes |-> "none"], reader |-> "owner",
                read |-> FALSE, visible |-> {}]

MergeNodes == {"source", "root"}
MergeDestinations == {"original", "destination", "standalone"}
MergeClock == [project: MergeDestinations, threadRevision: 0..3,
               projectRevision: 0..3, generation: 0..1]
InitialClock == [project |-> "original", threadRevision |-> 0,
                 projectRevision |-> 0, generation |-> 0]
EmptyMerge == [phase |-> "idle", liveClock |-> InitialClock,
               expected |-> InitialClock, admitted |-> InitialClock,
               staged |-> {}, shared |-> {}, published |-> {}]

Init ==
    /\ branch \in {"registry", "ledger", "promotion"}
    /\ ledger = EmptyLedger /\ merge = EmptyMerge
    /\ phase = "running"
    /\ live = {"parent"}
    /\ saved = {}
    /\ binding = Chosen /\ approved = Chosen
    /\ written = 0 /\ published = 0
    /\ lateWrite = FALSE /\ foreignEvidence = FALSE
    /\ restored = FALSE /\ stopped = FALSE
    /\ sourceRequired = 0

(* Descendants enter the same Registry and global budget as their parent. *)
Delegate ==
    /\ phase = "running" /\ "parent" \in live /\ "child" \notin live
    /\ live' = live \cup {"child"}
    /\ UNCHANGED <<phase, saved, binding, written, published, lateWrite,
                   foreignEvidence, restored, stopped, sourceRequired, approved>>

(* Handoff freezes tools, checkpoints every descendant, then restores exactly
   those model bindings. No model catalog/default is consulted on restoration. *)
Freeze ==
    /\ phase = "running" /\ ~restored
    /\ phase' = "frozen" /\ saved' = live /\ live' = {}
    /\ UNCHANGED <<binding, written, published, lateWrite,
                   foreignEvidence, restored, stopped, sourceRequired, approved>>

Restore ==
    /\ phase = "frozen"
    /\ phase' = "running" /\ live' = saved /\ restored' = TRUE
    /\ binding' = IF RebindOnRestore
                   THEN [w \in Workers |-> "planner"] ELSE approved
    /\ UNCHANGED <<saved, written, published, lateWrite, foreignEvidence, stopped, sourceRequired, approved>>

Stop ==
    /\ phase \in {"running", "frozen"}
    /\ phase' = "stopped" /\ stopped' = TRUE
    /\ live' = IF CancelParentOnly THEN live \ {"parent"} ELSE {}
    /\ saved' = {}
    /\ UNCHANGED <<binding, written, published, lateWrite, foreignEvidence, restored, sourceRequired, approved>>

(* The write transaction checks owner/cancellation and project references.
   A stopped/deleted owner cannot be recreated by a late callback. *)
Commit ==
    /\ written < MaxWrites
    /\ (phase = "running" \/ (WriteAfterStop /\ phase = "stopped"))
    /\ written' = written + 1
    /\ lateWrite' = (lateWrite \/ phase # "running")
    /\ foreignEvidence' = (foreignEvidence \/ CrossProjectEvidence)
    /\ published' = IF PublishBeforeCommit THEN written + 2 ELSE published
    /\ UNCHANGED <<phase, live, saved, binding, restored, stopped, sourceRequired, approved>>

(* SSE revisions invalidate a snapshot. They never publish uncommitted data;
   reconnecting readers recover from the current transactional snapshot. *)
Refresh ==
    /\ published < written /\ published' = written
    /\ UNCHANGED <<phase, live, saved, binding, written, lateWrite,
                   foreignEvidence, restored, stopped, sourceRequired, approved>>

Delete ==
    /\ phase # "deleted"
    /\ phase' = "deleted" /\ live' = {} /\ saved' = {}
    /\ UNCHANGED <<binding, written, published, lateWrite, foreignEvidence, restored, stopped, sourceRequired, approved>>

(* Mutating a visible source in this project requires a committed revision.
   For a moved author this models the original project's revision. *)
MutateSource(skip) ==
    /\ sourceRequired = 0 /\ written < MaxWrites
    /\ sourceRequired' = written + 1
    /\ written' = IF skip THEN written ELSE sourceRequired'
    /\ UNCHANGED <<phase, live, saved, binding, published, lateWrite,
                   foreignEvidence, restored, stopped, approved>>

(* RecoverWorkerModels: an explicit human action while no worker is executing.
   Only the child executor model is unavailable in this bounded instance.
   The separate durable override keeps original selection and authority visible.
   The implementation flattens repeated overrides in one SQLite transaction. *)
AuthorizeRecovery ==
    /\ phase = "frozen" /\ "child" \in saved
    /\ approved' = [approved EXCEPT !["child"] = "planner"]
    /\ UNCHANGED <<phase, live, saved, binding, written, published, lateWrite,
                   foreignEvidence, restored, stopped, sourceRequired>>

RegistryStep == AuthorizeRecovery \/ Delegate \/ Freeze \/ Restore \/ Stop
    \/ Commit \/ Refresh \/ Delete
    \/ MutateSource(RewindSkipsRevision) \/ MutateSource(DeleteMovedSkipsRevision)

CreatePrivate(node) ==
    /\ ledger.scope[node] = "none"
    /\ \A source \in Dependencies(node) : ledger.scope[source] # "none"
    /\ ledger' = [ledger EXCEPT !.scope[node] = "thread"]

(* The fixed graph has a one-edge prerequisite closure. Project creation can
   publish the root and its private prerequisite in the same operation. *)
Promote ==
    /\ ledger.scope["source"] # "none" /\ ledger.scope["root"] # "project"
    /\ ledger' = [ledger EXCEPT !.scope["root"] = "project",
                     !.scope["source"] = IF PromoteWithoutClosure THEN @ ELSE "project"]

ReadWork(thread) ==
    /\ ~ledger.read
    /\ ledger' = [ledger EXCEPT !.reader = thread, !.read = TRUE,
        !.visible = {node \in Nodes :
          \/ ledger.scope[node] = "project" /\ thread \in ProjectThreads
          \/ ledger.scope[node] = "thread" /\ (NodeThread(node) = thread \/ PrivateLeak)}]

LedgerStep ==
    /\ ~ledger.read
    /\ (\E node \in Nodes : CreatePrivate(node)) \/ Promote
        \/ (\E thread \in Threads : ReadWork(thread))

(* Human promotion begins with an already durable two-record private chain.
   Each admitted compare-and-swap transaction uses one current clock. Its
   writes and audit revisions are private until commit, including on error. *)
CaptureMergeSnapshot ==
    /\ merge.phase = "idle"
    /\ merge' = [merge EXCEPT !.phase = "confirmed", !.expected = merge.liveClock]

EditPrivateWork ==
    /\ merge.liveClock.threadRevision = 0
    /\ merge' = [merge EXCEPT !.liveClock.threadRevision = 1]

EditProjectWork ==
    /\ merge.liveClock.project # "standalone" /\ merge.liveClock.projectRevision = 0
    /\ merge' = [merge EXCEPT !.liveClock.projectRevision = 1]

MoveOwner(destination) ==
    /\ merge.liveClock.project = "original"
    /\ merge' = [merge EXCEPT !.liveClock.project = destination,
                       !.liveClock.threadRevision = @ + 1, !.liveClock.projectRevision = 0]

RestoreSameCounters ==
    /\ merge.liveClock.generation = 0
    /\ merge' = [merge EXCEPT !.liveClock.generation = 1]

MergeVersionsMatch ==
    LET captured == merge.expected
        actual == merge.liveClock
        \* The move fault replaces the captured project and its revision pair
        \* with live values. A real move always advances the thread revision.
        ignoredMove == PromoteIgnoresMove /\ captured.project # actual.project
    IN /\ (captured.project = actual.project \/ ignoredMove)
       /\ (captured.threadRevision = actual.threadRevision \/ PromoteIgnoresThreadRevision \/ ignoredMove)
       /\ (captured.projectRevision = actual.projectRevision \/ PromoteIgnoresProjectRevision \/ ignoredMove)
       /\ (captured.generation = actual.generation \/ PromoteIgnoresDataGeneration)

AdmitMerge ==
    /\ merge.phase = "confirmed"
    /\ merge' = [merge EXCEPT !.admitted = merge.liveClock,
        !.phase = IF ~MergeVersionsMatch THEN "conflict"
                  ELSE IF merge.liveClock.project = "standalone" THEN "no-project"
                  ELSE "writing"]

StageMergeNode(node) ==
    /\ merge.phase = "writing" /\ node \notin merge.staged
    /\ (node = "source" \/ "source" \in merge.staged \/ PromoteWithoutClosure)
    /\ merge' = [merge EXCEPT !.staged = @ \cup {node}]

CommitMerge ==
    /\ merge.phase = "writing" /\ "root" \in merge.staged
    /\ merge' = [merge EXCEPT !.phase = "committed", !.shared = merge.staged,
        !.liveClock.threadRevision = @ + 1,
        !.liveClock.projectRevision = @ + Cardinality(merge.staged)]

AbortMerge ==
    /\ merge.phase = "writing" /\ merge.staged # {}
    /\ merge' = [merge EXCEPT !.phase = "failed",
        !.shared = IF PromotePartiallyCommits THEN merge.staged ELSE @,
        !.liveClock.projectRevision = IF PromotePartiallyCommits THEN @ + Cardinality(merge.staged) ELSE @]

PublishMerge ==
    /\ merge.published = {}
    /\ (merge.phase = "committed" \/ (PromotePublishesStaged /\ merge.phase = "writing" /\ merge.staged # {}))
    /\ merge' = [merge EXCEPT !.published = IF merge.phase = "committed" THEN merge.shared ELSE merge.staged]

MergeStep ==
    \/ CaptureMergeSnapshot \/ AdmitMerge \/ CommitMerge \/ AbortMerge \/ PublishMerge
    \/ \E node \in MergeNodes : StageMergeNode(node)
    \/ /\ merge.phase \in {"idle", "confirmed"}
       /\ (EditPrivateWork \/ EditProjectWork \/ RestoreSameCounters
            \/ (\E destination \in MergeDestinations \ {"original"} : MoveOwner(destination)))

Next ==
    \/ /\ branch = "registry" /\ RegistryStep /\ UNCHANGED <<branch, ledger, merge>>
    \/ /\ branch = "ledger" /\ LedgerStep /\ UNCHANGED <<branch, registry, merge>>
    \/ /\ branch = "promotion" /\ MergeStep /\ UNCHANGED <<branch, registry, ledger>>
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ branch \in {"registry", "ledger", "promotion"}
    /\ merge \in [phase: {"idle", "confirmed", "writing", "committed", "conflict", "no-project", "failed"},
                 liveClock: MergeClock, expected: MergeClock, admitted: MergeClock,
                 staged: SUBSET MergeNodes, shared: SUBSET MergeNodes, published: SUBSET MergeNodes]
    /\ ledger \in [scope: [Nodes -> Scopes], reader: Threads, read: BOOLEAN,
                   visible: SUBSET Nodes]
    /\ phase \in {"running", "frozen", "stopped", "deleted"}
    /\ live \subseteq Workers /\ saved \subseteq Workers
    /\ binding \in [Workers -> Models] /\ approved \in [Workers -> Models]
    /\ written \in 0..MaxWrites /\ published \in 0..(MaxWrites + 1)
    /\ lateWrite \in BOOLEAN /\ foreignEvidence \in BOOLEAN
    /\ restored \in BOOLEAN /\ stopped \in BOOLEAN
    /\ sourceRequired \in 0..MaxWrites

BindingStable == \A w \in Workers: binding[w] \in {Chosen[w], approved[w]}
StopOwnsDescendants == stopped => live = {}
NoLateFact == ~lateWrite
ProjectScoped == ~foreignEvidence
VisibleIsCommitted == published <= written
SourceChangeHasRevision == sourceRequired <= written

ProjectDependencyClosure ==
    \A node \in Nodes : ledger.scope[node] = "project" =>
        \A source \in Dependencies(node) : ledger.scope[source] = "project"
PrivateWorkIsOwned ==
    ledger.read => \A node \in ledger.visible :
        ledger.scope[node] = "thread" => NodeThread(node) = ledger.reader
NeverPeerSeesPromotion ==
    ~(ledger.read /\ ledger.reader = "peer" /\ {"root", "source"} \subseteq ledger.visible)
NeverStandaloneReadsOwn ==
    ~(ledger.read /\ ledger.reader = "standalone" /\ "standalone-record" \in ledger.visible)

MergeAdmissionUsesSnapshot ==
    merge.phase \in {"writing", "committed", "failed"} =>
        merge.expected = merge.admitted /\ merge.admitted.project # "standalone"

MergeCommitsClosureAndRevisions ==
    merge.phase = "committed" =>
        /\ merge.shared = MergeNodes
        /\ merge.liveClock = [merge.admitted EXCEPT !.threadRevision = @ + 1,
                               !.projectRevision = @ + Cardinality(MergeNodes)]

MergeRollbackKeepsCommittedState ==
    merge.phase \in {"failed", "conflict", "no-project"} =>
        merge.shared = {} /\ merge.liveClock = merge.admitted

MergePublicationIsCommitted == merge.published \subseteq merge.shared

NeverCommittedMergeIsVisible ==
    ~(merge.phase = "committed" /\ merge.published = MergeNodes)
NeverMergeRollsBackFirstWrite ==
    ~(merge.phase = "failed" /\ merge.staged = {"source"})
NeverMergeRejectsStaleThread ==
    ~(merge.phase = "conflict" /\ merge.admitted = [merge.expected EXCEPT !.threadRevision = @ + 1])
NeverMergeRejectsStaleProject ==
    ~(merge.phase = "conflict" /\ merge.admitted = [merge.expected EXCEPT !.projectRevision = @ + 1])
NeverMergeRejectsMovedOwner ==
    ~(merge.phase = "conflict" /\ merge.expected.project = "original" /\ merge.admitted.project = "destination")
NeverMergeRejectsRestore ==
    ~(merge.phase = "conflict" /\ merge.admitted = [merge.expected EXCEPT !.generation = @ + 1])
NeverMergeRefusesStandalone ==
    ~(merge.phase = "no-project" /\ merge.expected.project = "standalone")

NeverRestored == ~restored
NeverSharedResult == written = 0 \/ sourceRequired > 0
NeverDelegated == "child" \notin live
=============================================================================
