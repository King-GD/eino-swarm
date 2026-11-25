---------------------------- MODULE EASMCollection ----------------------------
EXTENDS Naturals, FiniteSets

(* One panel, three steps, two queries and at most two serial source calls.
   The producer's fourth step, clouds, is not modeled separately: like
   components it has no partial-result semantics, assembles its replies
   off-lock (its per-provider requests and total check stay inside the step)
   and applies once before the shared outcome/flush, so it adds states but
   no new interleaving. A per-row publication would change that and needs
   the model first.
   Variant 1 checks query/retry decisions; variant 2 checks concurrent steps,
   cancellation and failed writes. Every variant checks the same safety list.

   run.apply and the step's outcome/flush are separate critical sections.
   Another step may publish an applied result while its status is still running.
   A flush keeps mu until its synchronous publisher returns, but cancellation
   and remote replies can occur while it holds that lock. SQLite publication
   is abstracted as success/failure; this is not an atomicity proof for Go/SQL.

   collect's final "done" means settled, not that every step succeeded.
   Stop's snapshot and the host's terminal marker may both fail to persist.
   Loading settles only after an observed terminal source and an inactive task.
   No fairness, eventual write success, process recovery or wall-clock bound is
   assumed. Source ownership and projection ordering have separate models.

   ClaimCompleteOnSettled and TrustStoredRunning re-find 49377286's fixes.
   MergePartialTotals seeds completion from cumulative partial replies; this is
   not the earlier producer's different any-total-is-success implementation.
   Other switches remove one protocol guard and need implementation mutations.
   See run_test.go, retry_test.go, receipt_test.go and lib/easm/data.test.ts. *)
CONSTANTS Variant, KeepOldQueryData, RetryCompleted, MergePartialTotals,
          ClaimCompleteOnSettled, ReturnBeforeJoin, PublishUnlocked,
          InventFailedPersistence, TrustStoredRunning, CollectWithoutQuery

ASSUME Variant \in 1..2
ASSUME /\ KeepOldQueryData \in BOOLEAN /\ RetryCompleted \in BOOLEAN
       /\ MergePartialTotals \in BOOLEAN /\ ClaimCompleteOnSettled \in BOOLEAN
       /\ ReturnBeforeJoin \in BOOLEAN /\ PublishUnlocked \in BOOLEAN
       /\ InventFailedPersistence \in BOOLEAN /\ TrustStoredRunning \in BOOLEAN
       /\ CollectWithoutQuery \in BOOLEAN

Steps == {"summary", "heat", "components"}
Totals == {"assets", "ips"}
Statuses == {"pending", "running", "done", "failed", "unavailable", "interrupted"}
Stages == {"skip", "waiting", "call", "reply", "applied", "settled", "joined"}
Replies == {"none", "full", "assets", "ips", "failed", "unavailable"}
Empty == [query |-> 0, data |-> [s \in Steps |-> 0], totals |-> {},
          summaryComplete |-> FALSE, status |-> [s \in Steps |-> "pending"],
          collection |-> "idle"]
Done(m) == {s \in Steps : m.status[s] = "done"}
AllDone(m) == Done(m) = Steps
Joined(v) == \A s \in v.calls : v.stage[s] = "joined"
Settled(v) == \A s \in Steps : v.stage[s] \in {"skip", "joined"}
SnapshotType == [query : 0..2, data : [Steps -> 0..2], totals : SUBSET Totals,
                 summaryComplete : BOOLEAN, status : [Steps -> Statuses],
                 collection : {"idle", "running", "done", "interrupted"}]

VARIABLE v
vars == <<v>>

Init == v = [attempt |-> 0, phase |-> "idle", requested |-> 0,
    mem |-> Empty, durable |-> Empty, observed |-> Empty,
    stage |-> [s \in Steps |-> "skip"], reply |-> [s \in Steps |-> "none"],
    kept |-> {}, calls |-> {}, cancelled |-> FALSE, taskActive |-> FALSE,
    pending |-> FALSE, captured |-> Empty, publishKind |-> "initial",
    publisher |-> "summary", failedBase |-> Empty, lastFailed |-> FALSE,
    publishError |-> FALSE, interruptFailed |-> FALSE, hostWriteFailed |-> FALSE,
    source |-> "idle", observedSource |-> "idle", toolOK |-> FALSE,
    completionClaim |-> FALSE]

(* begin/planMetrics reads the last committed panel, never an uncommitted result.
   No-query batches retain the old query and its data but start no collection. *)
Begin(q) ==
    /\ ~v.pending /\ v.phase \in {"idle", "returned"}
    /\ ((v.attempt = 0 /\ q = 1) \/ (Variant = 1 /\ v.attempt = 1 /\ q \in 0..2))
    /\ LET changed == q # 0 /\ q # v.durable.query
           keep == IF changed THEN {} ELSE Done(v.durable)
           todo == IF q = 0 /\ ~CollectWithoutQuery THEN {} ELSE
                   IF changed \/ RetryCompleted THEN Steps ELSE Steps \ keep
           base == IF changed THEN
                     [v.durable EXCEPT !.query = q,
                         !.data = IF KeepOldQueryData THEN @ ELSE [s \in Steps |-> 0],
                         !.totals = IF KeepOldQueryData THEN @ ELSE {},
                         !.summaryComplete = IF KeepOldQueryData THEN @ ELSE FALSE]
                   ELSE v.durable
           planned == [base EXCEPT
               !.status = [s \in Steps |-> IF s \in todo THEN "running" ELSE base.status[s]],
               !.collection = IF todo # {} THEN "running" ELSE
                              IF AllDone(base) THEN "done" ELSE "idle"]
       IN v' = [v EXCEPT !.attempt = @ + 1, !.phase = "collecting", !.requested = q,
            !.mem = planned, !.stage = [s \in Steps |-> IF s \in todo THEN "waiting" ELSE "skip"],
            !.reply = [s \in Steps |-> "none"], !.kept = keep, !.calls = {},
            !.cancelled = FALSE, !.taskActive = TRUE, !.pending = TRUE,
            !.captured = planned, !.publishKind = "initial", !.lastFailed = FALSE,
            !.publishError = FALSE, !.interruptFailed = FALSE, !.hostWriteFailed = FALSE,
            !.source = "running", !.toolOK = FALSE, !.completionClaim = FALSE]

Start(s) ==
    /\ v.phase = "collecting" /\ ~v.pending /\ ~v.cancelled /\ v.stage[s] = "waiting"
    /\ v' = [v EXCEPT !.stage[s] = "call", !.calls = @ \cup {s}]

(* Reply completion does not mutate shared panel state or acquire its mutex.
   Query/retry variant first sees partial totals and an unavailable trend;
   a same-query retry sees the other partial total, a changed query sees full data. *)
Reply(s) ==
    /\ v.phase = "collecting" /\ v.stage[s] = "call"
    /\ LET result == IF Variant = 2 THEN "full" ELSE
                       IF s = "summary" THEN
                           IF v.attempt = 1 THEN "assets" ELSE
                           IF v.requested = 1 THEN "ips" ELSE "full"
                       ELSE IF s = "heat" /\ v.attempt = 1 THEN "unavailable" ELSE "full"
       IN v' = [v EXCEPT !.stage[s] = "reply", !.reply[s] = result]

(* A successful apply writes data once; its status still belongs to finalize.
   Products and their optional names have already been assembled off-lock. *)
Apply(s) ==
    /\ v.phase = "collecting" /\ v.stage[s] = "reply"
    /\ (~v.pending \/ PublishUnlocked)
    /\ LET bits == CASE v.reply[s] = "full" -> Totals
                         [] v.reply[s] = "assets" -> {"assets"}
                         [] v.reply[s] = "ips" -> {"ips"}
                         [] OTHER -> {}
           data == v.reply[s] \in {"full", "assets", "ips"}
       IN v' = [v EXCEPT !.stage[s] = "applied",
            !.mem.data[s] = IF data THEN v.mem.query ELSE @,
            !.mem.totals = IF s = "summary" THEN @ \cup bits ELSE @,
            !.mem.summaryComplete = IF s = "summary" THEN v.reply[s] = "full" ELSE @]

Finalize(s) ==
    /\ v.phase = "collecting" /\ ~v.pending /\ v.stage[s] = "applied"
    /\ IF v.cancelled THEN v' = [v EXCEPT !.stage[s] = "settled"] ELSE
       LET outcome == CASE v.reply[s] = "full" -> "done"
                           [] s = "summary" /\ MergePartialTotals /\ v.mem.totals = Totals -> "done"
                           [] v.reply[s] = "unavailable" -> "unavailable"
                           [] OTHER -> "failed"
           m == [v.mem EXCEPT !.status[s] = outcome]
       IN v' = [v EXCEPT !.mem = m, !.captured = m, !.pending = TRUE,
                         !.publishKind = "step", !.publisher = s]

Join(s) ==
    /\ v.phase = "collecting" /\ v.stage[s] = "settled"
    /\ v' = [v EXCEPT !.stage[s] = "joined"]

Cancel ==
    /\ Variant = 2 /\ v.phase = "collecting" /\ ~v.cancelled
    /\ v' = [v EXCEPT !.cancelled = TRUE, !.taskActive = FALSE]

CancelWaiting(s) ==
    /\ v.phase = "collecting" /\ v.cancelled /\ v.stage[s] = "waiting"
    /\ v' = [v EXCEPT !.stage[s] = "joined"]

(* A normal final flush may settle a collection containing failed steps.
   Cancellation joins first, then attempts an interrupted snapshot on a detached
   context. A previous ordinary publication failure instead returns an error. *)
FinishSteps ==
    /\ v.phase = "collecting" /\ ~v.pending /\ Settled(v)
    /\ IF v.cancelled THEN
           LET m == [v.mem EXCEPT !.collection = "interrupted",
                    !.status = [s \in Steps |-> IF v.mem.status[s] \in {"pending", "running"}
                                               THEN "interrupted" ELSE v.mem.status[s]]]
           IN v' = [v EXCEPT !.mem = m, !.captured = m, !.pending = TRUE,
                             !.publishKind = "interrupt"]
       ELSE IF v.publishError THEN v' = [v EXCEPT !.phase = "host-interrupt"]
       ELSE LET m == [v.mem EXCEPT !.collection = "done"]
            IN v' = [v EXCEPT !.mem = m, !.captured = m, !.pending = TRUE,
                              !.publishKind = "final"]

(* The synchronous publish callback holds mu through success or failure.
   Success is an abstract committed publication; failure preserves that state.
   Failures are enabled independently of cancellation in the interleaving case. *)
Publish(ok) ==
    /\ v.pending /\ (ok \/ Variant = 2)
    /\ v' = [v EXCEPT
        !.durable = IF ok \/ InventFailedPersistence THEN v.captured ELSE @,
        !.failedBase = v.durable, !.lastFailed = ~ok, !.pending = FALSE,
        !.publishError = @ \/ ~ok,
        !.interruptFailed = @ \/ (~ok /\ v.publishKind = "interrupt"),
        !.stage = IF v.publishKind = "step" THEN [@ EXCEPT ![v.publisher] = "settled"] ELSE @,
        !.phase = CASE v.publishKind = "initial" ->
                           IF ~ok THEN "host-interrupt" ELSE
                           IF \A s \in Steps : v.stage[s] = "skip" THEN "source-finish" ELSE "collecting"
                     [] v.publishKind = "step" -> "collecting"
                     [] v.publishKind = "final" -> IF ok THEN "source-finish" ELSE "host-interrupt"
                     [] OTHER -> "host-interrupt"]

FinishSource(ok) ==
    /\ ~v.pending /\ v.phase = "source-finish" /\ (ok \/ Variant = 2)
    /\ v' = [v EXCEPT !.source = IF ok THEN "done" ELSE @,
                       !.hostWriteFailed = ~ok,
                       !.phase = IF ok THEN "returnable" ELSE "host-interrupt"]

(* The host's source marker is independent of the producer snapshot. Either
   write can fail; a failed cleanup leaves running in durable source metadata. *)
InterruptSource(ok) ==
    /\ ~v.pending /\ v.phase = "host-interrupt" /\ (ok \/ Variant = 2)
    /\ v' = [v EXCEPT !.source = IF ok THEN "interrupted" ELSE @,
                       !.hostWriteFailed = ~ok, !.phase = "returnable"]

Return ==
    /\ ~v.pending /\ (v.phase = "returnable" \/
                       (ReturnBeforeJoin /\ v.phase = "collecting" /\ ~Joined(v)))
    /\ v' = [v EXCEPT !.phase = "returned",
        !.toolOK = ~v.cancelled /\ ~v.publishError /\ v.source = "done",
        !.completionClaim = v.requested # 0 /\ ~v.cancelled /\ ~v.publishError /\ v.source = "done" /\
                             (AllDone(v.mem) \/ ClaimCompleteOnSettled)]

TaskInactive ==
    /\ v.phase = "returned" /\ v.taskActive
    /\ v' = [v EXCEPT !.taskActive = FALSE]

Observe ==
    v' = [v EXCEPT !.observed = v.durable, !.observedSource = v.source]

Next == (\E q \in 0..2 : Begin(q)) \/
        (\E s \in Steps : Start(s) \/ Reply(s) \/ Apply(s) \/ Finalize(s) \/ Join(s) \/ CancelWaiting(s)) \/
        (\E ok \in BOOLEAN : Publish(ok) \/ FinishSource(ok) \/ InterruptSource(ok)) \/
        Cancel \/ FinishSteps \/ Return \/ TaskInactive \/ Observe
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ v.attempt \in 0..2 /\ v.requested \in 0..2
    /\ v.phase \in {"idle", "collecting", "source-finish", "host-interrupt", "returnable", "returned"}
    /\ \A m \in {v.mem, v.durable, v.observed, v.captured, v.failedBase} : m \in SnapshotType
    /\ v.stage \in [Steps -> Stages] /\ v.reply \in [Steps -> Replies]
    /\ v.kept \subseteq Steps /\ v.calls \subseteq Steps
    /\ v.publisher \in Steps /\ v.publishKind \in {"initial", "step", "final", "interrupt"}
    /\ v.source \in {"idle", "running", "done", "interrupted"}
    /\ v.observedSource \in {"idle", "running", "done", "interrupted"}
    /\ \A b \in {v.cancelled, v.taskActive, v.pending, v.lastFailed, v.publishError,
                  v.interruptFailed, v.hostWriteFailed, v.toolOK, v.completionClaim} : b \in BOOLEAN

QueryDataOwned ==
    /\ \A s \in Steps : v.durable.data[s] # 0 => v.durable.data[s] = v.durable.query
    /\ v.durable.totals # {} => (v.durable.query # 0 /\ v.durable.data["summary"] = v.durable.query)
DoneStepsNotRepeated == v.calls \cap v.kept = {}
SummaryDoneHasWholeReply == v.mem.status["summary"] = "done" => v.mem.summaryComplete
CompletionClaimRequiresAllSteps == v.completionClaim => (v.toolOK /\ AllDone(v.mem))
ReturnWaitsForJoin == v.phase = "returned" => Joined(v)
LockedPublicationStable == v.pending => v.mem = v.captured
FailedPublishKeepsDurable == v.lastFailed => v.durable = v.failedBase
NoQueryStartsNoCalls == v.requested = 0 => v.calls = {}

Loading(s) == IF TrustStoredRunning THEN
                 v.observed.status[s] = "running" \/ v.observed.collection = "running"
             ELSE (v.observedSource = "running" /\ v.observed.status[s] \in {"pending", "running"})
                  \/ (v.taskActive /\ v.observed.status[s] # "done")
TerminalObservationSettles ==
    v.observedSource \in {"done", "interrupted"} /\ ~v.taskActive => \A s \in Steps : ~Loading(s)

(* Negated witnesses must fail; none claims liveness or durable Stop success. *)
NeverRetryKeepsDone == ~(v.attempt = 2 /\ v.requested = 1 /\ v.phase = "returned" /\
                         "components" \in v.kept /\ "components" \notin v.calls)
NeverPartialSummaryRetried == ~(v.attempt = 2 /\ v.requested = 1 /\ v.phase = "returned" /\
                               v.mem.totals = Totals /\ v.mem.status["summary"] = "failed")
NeverChangedQueryCleared == ~(v.attempt = 2 /\ v.requested = 2 /\ v.durable.query = 2 /\
                             \A s \in Steps : v.durable.data[s] = 0)
NeverUnqueriedBatch == ~(v.attempt = 2 /\ v.requested = 0 /\ v.phase = "returned" /\ v.calls = {})
NeverSettledWithMissing == ~(v.toolOK /\ v.mem.collection = "done" /\ ~AllDone(v.mem) /\ ~v.completionClaim)
NeverAllSucceeded == ~(v.phase = "returned" /\ v.completionClaim /\ AllDone(v.mem))
NeverAppliedBeforeOutcome == ~(\E s \in Steps : v.durable.data[s] # 0 /\ v.durable.status[s] = "running")
NeverFailedPublication == ~v.lastFailed
NeverFailedFinishReturns == ~(v.phase = "returned" /\ ~v.cancelled /\ ~v.publishError /\
                             v.mem.collection = "done" /\ v.source # "done" /\
                             ~v.toolOK /\ ~v.completionClaim)
NeverBothInterruptWritesFail == ~(v.phase = "returned" /\ v.cancelled /\ v.interruptFailed /\ v.hostWriteFailed)
NeverInterruptedReload == ~(~v.taskActive /\ v.observedSource = "interrupted" /\
                           v.observed.collection = "running")
=============================================================================
