------------------------------ MODULE TaskPanel ------------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS
    IgnoreOwner,
    DeliverBeforeCommit,
    ApplyDuplicate,
    IgnoreBaseRevision,
    IgnoreOperationDigest,
    CleanupOtherOwner,
    SkipRecovery,
    SeparatePanelConnection

(* The publication revision below counts accepted data operations, abstracting
   away lifecycle-only wire revision increments. Two operation IDs and digests
   distinguish retries from independent updates and changed-body reuse. Prepare
   captures one writer's base before CompetingPublish can commit another write.
   The separate ownership model covers the source execution lifetime. *)
Operations == 1..2
Digests == 1..2
VARIABLES exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs
vars == <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Init == /\ exists = TRUE /\ stopped = FALSE /\ revision = 0
        /\ committed = 0 /\ delivered = 0 /\ accepted = {} /\ digests = [op \in Operations |-> 0]
        /\ pending = FALSE /\ operation = 1 /\ digest = 1 /\ base = 0
        /\ badBase = FALSE /\ badDigest = FALSE /\ competed = FALSE /\ lateWrite = FALSE
        /\ successor = FALSE /\ cleaned = FALSE /\ wrongCleanup = FALSE
        /\ sourcePhase = "idle" /\ restored = FALSE /\ connectedTabs = {}
Prepare(op, body) ==
    /\ ~pending /\ revision < 3 /\ op \in Operations /\ body \in Digests
    /\ pending' = TRUE /\ operation' = op /\ digest' = body /\ base' = revision
    /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted,
                   digests, badBase, badDigest, competed, lateWrite, successor,
                   cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Publish ==
    /\ pending /\ revision < 3
    /\ ((exists /\ ~stopped) \/ IgnoreOwner)
    /\ IF operation \in accepted
       THEN digests[operation] = digest \/ IgnoreOperationDigest
       ELSE base = revision \/ IgnoreBaseRevision
    /\ LET changes == operation \notin accepted \/ ApplyDuplicate
       IN /\ revision' = IF changes THEN revision + 1 ELSE revision
          /\ committed' = IF changes THEN revision + 1 ELSE committed
    /\ accepted' = accepted \cup {operation}
    /\ digests' = IF operation \notin accepted
                  THEN [digests EXCEPT ![operation] = digest] ELSE digests
    /\ badBase' = (badBase \/ (operation \notin accepted /\ base # revision))
    /\ badDigest' = (badDigest \/ (operation \in accepted /\ digests[operation] # digest))
    /\ pending' = FALSE /\ lateWrite' = (lateWrite \/ ~exists \/ stopped)
    /\ UNCHANGED <<exists, stopped, delivered, operation, digest, base, competed,
                   successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Refuse ==
    /\ pending
    /\ (~(exists /\ ~stopped)
        \/ (operation \in accepted /\ digests[operation] # digest)
        \/ (operation \notin accepted /\ base # revision))
    /\ pending' = FALSE
    /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted,
                   digests, operation, digest, base, badBase, badDigest, competed,
                   lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
CompetingPublish(op) ==
    /\ pending /\ exists /\ ~stopped /\ op \in Operations \ accepted /\ revision < 3
    /\ revision' = revision + 1 /\ committed' = revision + 1
    /\ accepted' = accepted \cup {op} /\ digests' = [digests EXCEPT ![op] = op]
    /\ competed' = TRUE
    /\ UNCHANGED <<exists, stopped, delivered, pending, operation, digest, base,
                   badBase, badDigest, lateWrite, successor, cleaned, wrongCleanup,
                   sourcePhase, restored, connectedTabs>>
Deliver == /\ delivered < 3
           /\ (delivered < committed \/ DeliverBeforeCommit)
           /\ delivered' = delivered + 1
           /\ UNCHANGED <<exists, stopped, revision, committed, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Stop == /\ ~stopped /\ stopped' = TRUE
        /\ UNCHANGED <<exists, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Delete == /\ exists /\ exists' = FALSE
          /\ UNCHANGED <<stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Replace == /\ ~successor /\ successor' = TRUE
           /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, cleaned, wrongCleanup, sourcePhase, restored, connectedTabs>>
Cleanup == /\ ~cleaned /\ exists /\ stopped
           /\ (~successor \/ CleanupOtherOwner)
           /\ cleaned' = TRUE /\ wrongCleanup' = successor
           /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, sourcePhase, restored, connectedTabs>>
BeginSource == /\ ~restored /\ sourcePhase = "idle" /\ sourcePhase' = "running"
               /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, restored, connectedTabs>>
FinishSource == /\ sourcePhase = "running" /\ sourcePhase' = "finished"
                /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, restored, connectedTabs>>
Restore == /\ ~restored /\ restored' = TRUE
           /\ sourcePhase' = IF sourcePhase = "running" /\ ~SkipRecovery THEN "interrupted" ELSE sourcePhase
           /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, connectedTabs>>
(* Two ordinary browser windows already hold presence and chat streams.
   A separate panel SSE consumes both remaining H1 slots, blocking draft PUT.
   Multiplexed panel frames allocate no extra physical connections. *)
Connections == 2 * Cardinality(connectedTabs) +
               (IF SeparatePanelConnection THEN Cardinality(connectedTabs) ELSE 0)
Connect(tab) == /\ tab \in 1..2 /\ tab \notin connectedTabs
                /\ connectedTabs' = connectedTabs \cup {tab}
                /\ UNCHANGED <<exists, stopped, revision, committed, delivered, accepted, digests, pending, operation, digest, base, badBase, badDigest, competed, lateWrite, successor, cleaned, wrongCleanup, sourcePhase, restored>>
Next == (\E tab \in 1..2 : Connect(tab))
        \/ (\E op \in Operations, body \in Digests : Prepare(op, body))
        \/ (\E op \in Operations : CompetingPublish(op)) \/ Refuse \/ Publish \/ Deliver \/ Stop \/ Delete \/ Replace \/ Cleanup \/ BeginSource \/ FinishSource \/ Restore
TypeOK == /\ connectedTabs \subseteq 1..2
          /\ exists \in BOOLEAN /\ stopped \in BOOLEAN /\ accepted \subseteq Operations /\ digests \in [Operations -> 0..2]
          /\ pending \in BOOLEAN /\ operation \in Operations /\ digest \in Digests
          /\ base \in 0..3 /\ badBase \in BOOLEAN /\ badDigest \in BOOLEAN /\ competed \in BOOLEAN
          /\ restored \in BOOLEAN /\ sourcePhase \in {"idle", "running", "finished", "interrupted"}
          /\ lateWrite \in BOOLEAN /\ successor \in BOOLEAN /\ cleaned \in BOOLEAN /\ wrongCleanup \in BOOLEAN /\ revision \in 0..3 /\ committed \in 0..3 /\ delivered \in 0..3
CommittedBeforeDelivery == delivered <= committed
OwnerRequired == ~lateWrite
Idempotent == revision = Cardinality(accepted)
BaseRevisionRequired == ~badBase
OperationBodyStable == ~badDigest
CleanupKeepsOwner == ~wrongCleanup
NoOrphanAfterRestore == restored => sourcePhase # "running"
SaveConnectionAvailable == Connections < 6
NeverPublished == revision = 0
NeverCompetingWriter == ~competed
NeverRestored == ~restored
NeverTwoWindows == Cardinality(connectedTabs) < 2
Spec == Init /\ [][Next]_vars
=============================================================================
