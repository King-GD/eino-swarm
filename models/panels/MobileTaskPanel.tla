------------------------- MODULE MobileTaskPanel ---------------------------
(***************************************************************************)
(* A finite refinement of the existing late-visit and mixed-snapshot fault  *)
(* classes into a two-chunk mobile transfer. A transfer owns one immutable  *)
(* thread/panel/revision/body/hash/token even while manifests advance.      *)
(*                                                                         *)
(* Fault switches are seeded hypotheses until a real sealed-RPC or client  *)
(* mutation regression demonstrates the corresponding defect. TLC does not *)
(* prove base64/frame-size arithmetic, hashing, RPC implementation, browser  *)
(* isolation or operating-system behavior. There is no network liveness     *)
(* claim: refresh cancellation is a safety guard against starvation.        *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    MixTransferRevision,
    AcceptLateVisit,
    ApplyPartialBody,
    CloseOtherTransfer,
    UsePanelCursorForChat,
    RefreshCancelsTransfer

ASSUME /\ MixTransferRevision \in BOOLEAN
       /\ AcceptLateVisit \in BOOLEAN
       /\ ApplyPartialBody \in BOOLEAN
       /\ CloseOtherTransfer \in BOOLEAN
       /\ UsePanelCursorForChat \in BOOLEAN
       /\ RefreshCancelsTransfer \in BOOLEAN

Chunks == {1, 2}
Checks == {"unchecked", "valid", "bad_hash", "bad_identity", "bad_base"}
EmptyTransfer == [token |-> 0, visit |-> 0, link |-> 0, thread |-> 0,
                  panel |-> 0, revision |-> 0, kind |-> "snapshot", base |-> 0]
EmptyReply == [token |-> 0, visit |-> 0, link |-> 0, thread |-> 0,
               index |-> 1, payloadRevision |-> 0]

VARIABLES manifestRevision, lastToken, transfer, pending, chunks, chunkRevision,
          checked, applied, clientRevision, visit, link, thread, chatSeq,
          closedSuccessor, refreshCancelled, sawRefresh, seenSnapshot,
          seenPatch, recoveredVisit

vars == <<manifestRevision, lastToken, transfer, pending, chunks, chunkRevision,
          checked, applied, clientRevision, visit, link, thread, chatSeq,
          closedSuccessor, refreshCancelled, sawRefresh, seenSnapshot,
          seenPatch, recoveredVisit>>

Init ==
    /\ manifestRevision = 1 /\ lastToken = 0
    /\ transfer = EmptyTransfer /\ pending = EmptyReply
    /\ chunks = {} /\ chunkRevision = [i \in Chunks |-> 0]
    /\ checked = "unchecked" /\ applied = FALSE /\ clientRevision = 0
    /\ visit = 1 /\ link = 1 /\ thread = 1 /\ chatSeq = 0
    /\ closedSuccessor = FALSE /\ refreshCancelled = FALSE
    /\ sawRefresh = FALSE /\ seenSnapshot = FALSE
    /\ seenPatch = FALSE /\ recoveredVisit = FALSE

TransferScope == transfer.link = link /\ transfer.thread = thread /\
                 transfer.panel = thread
CurrentOwner == transfer.visit = visit /\ TransferScope
ReplyOwnsTransfer == pending.token = transfer.token /\ transfer.token # 0
ReplyScope == pending.link = link /\ pending.thread = thread
ReplyOwnsVisit == pending.visit = visit /\ ReplyScope

(* Read-only open freezes one complete Panel snapshot or one Change patch. *)
Open(kind) ==
    /\ lastToken < 2
    /\ (transfer.token = 0 \/ applied \/ ~CurrentOwner \/
        checked \in Checks \ {"unchecked", "valid"})
    /\ kind \in {"snapshot", "patch"}
    /\ (kind = "snapshot" \/ (clientRevision = 1 /\ manifestRevision = 2))
    /\ lastToken' = lastToken + 1
    /\ transfer' = [token |-> lastToken + 1, visit |-> visit, link |-> link,
                    thread |-> thread, panel |-> thread,
                    revision |-> manifestRevision, kind |-> kind,
                    base |-> IF kind = "patch" THEN 1 ELSE 0]
    /\ chunks' = {} /\ chunkRevision' = [i \in Chunks |-> 0]
    /\ checked' = "unchecked" /\ applied' = FALSE
    /\ UNCHANGED <<manifestRevision, pending, clientRevision, visit, link,
                   thread, chatSeq, closedSuccessor, refreshCancelled,
                   sawRefresh, seenSnapshot, seenPatch, recoveredVisit>>

RequestChunk(i) ==
    /\ transfer.token # 0 /\ pending.token = 0 /\ i \in Chunks \ chunks
    /\ pending' = [token |-> transfer.token, visit |-> transfer.visit,
                   link |-> transfer.link, thread |-> transfer.thread,
                   index |-> i,
                   payloadRevision |-> IF MixTransferRevision
                                       THEN manifestRevision
                                       ELSE transfer.revision]
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, chunks,
                   chunkRevision, checked, applied, clientRevision,
                   visit, link, thread, chatSeq, closedSuccessor,
                   refreshCancelled, sawRefresh, seenSnapshot,
                   seenPatch, recoveredVisit>>

DeliverChunk ==
    /\ pending.token # 0 /\ ReplyOwnsTransfer
    /\ ReplyScope /\ (pending.visit = visit \/ AcceptLateVisit)
    /\ chunks' = chunks \cup {pending.index}
    /\ chunkRevision' = [chunkRevision EXCEPT
                         ![pending.index] = pending.payloadRevision]
    /\ pending' = EmptyReply
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, checked, applied,
                   clientRevision, visit, link, thread, chatSeq,
                   closedSuccessor, refreshCancelled, sawRefresh,
                   seenSnapshot, seenPatch, recoveredVisit>>

DiscardLateReply ==
    /\ pending.token # 0
    /\ (~ReplyOwnsTransfer \/ ~ReplyScope \/
        (pending.visit # visit /\ ~AcceptLateVisit))
    /\ pending' = EmptyReply
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, chunks,
                   chunkRevision, checked, applied, clientRevision,
                   visit, link, thread, chatSeq, closedSuccessor,
                   refreshCancelled, sawRefresh, seenSnapshot,
                   seenPatch, recoveredVisit>>

CheckEnvelope(check) ==
    /\ transfer.token # 0 /\ chunks = Chunks /\ checked = "unchecked"
    /\ check \in Checks \ {"unchecked"}
    /\ (check # "valid" \/
        (\A i \in chunks : chunkRevision[i] = transfer.revision))
    /\ checked' = check
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, pending, chunks,
                   chunkRevision, applied, clientRevision, visit, link,
                   thread, chatSeq, closedSuccessor, refreshCancelled,
                   sawRefresh, seenSnapshot, seenPatch, recoveredVisit>>

Apply ==
    /\ transfer.token # 0 /\ ~applied
    /\ TransferScope /\ (transfer.visit = visit \/ AcceptLateVisit)
    /\ ((chunks = Chunks /\ checked = "valid" /\
         (transfer.kind = "snapshot" \/ transfer.base = clientRevision)) \/
        (ApplyPartialBody /\ chunks # {} /\ chunks # Chunks))
    /\ applied' = TRUE /\ clientRevision' = transfer.revision
    /\ chatSeq' = IF UsePanelCursorForChat THEN transfer.revision ELSE chatSeq
    /\ seenSnapshot' = (seenSnapshot \/ transfer.kind = "snapshot")
    /\ seenPatch' = (seenPatch \/ transfer.kind = "patch")
    /\ recoveredVisit' = (recoveredVisit \/
                          (visit > 1 /\ CurrentOwner /\ transfer.kind = "snapshot"))
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, pending, chunks,
                   chunkRevision, checked, visit, link, thread,
                   closedSuccessor, refreshCancelled, sawRefresh>>

(* Three visits include A -> B -> A: matching IDs do not restore an old visit. *)
SwitchVisit ==
    /\ visit < 3 /\ visit' = visit + 1
    /\ link' = IF link = 1 THEN 2 ELSE 1
    /\ thread' = IF thread = 1 THEN 2 ELSE 1
    /\ applied' = FALSE /\ clientRevision' = 0
    /\ UNCHANGED <<manifestRevision, lastToken, transfer, pending, chunks,
                   chunkRevision, checked, chatSeq, closedSuccessor,
                   refreshCancelled, sawRefresh, seenSnapshot,
                   seenPatch, recoveredVisit>>

RefreshManifest ==
    /\ manifestRevision = 1 /\ manifestRevision' = 2
    /\ sawRefresh' = TRUE
    /\ LET cancel == RefreshCancelsTransfer /\ transfer.token # 0 /\ ~applied
       IN /\ transfer' = IF cancel THEN [transfer EXCEPT !.token = 0]
                         ELSE transfer
          /\ refreshCancelled' = (refreshCancelled \/ cancel)
    /\ UNCHANGED <<lastToken, pending, chunks, chunkRevision, checked, applied,
                   clientRevision, visit, link, thread, chatSeq,
                   closedSuccessor, seenSnapshot, seenPatch, recoveredVisit>>

(* A delayed close for token 1 must never remove successor token 2. *)
CloseOld ==
    /\ transfer.token = 2 /\ ~closedSuccessor
    /\ CloseOtherTransfer
    /\ transfer' = [transfer EXCEPT !.token = 0]
    /\ closedSuccessor' = TRUE
    /\ UNCHANGED <<manifestRevision, lastToken, pending, chunks, chunkRevision,
                   checked, applied, clientRevision, visit, link, thread,
                   chatSeq, refreshCancelled, sawRefresh, seenSnapshot,
                   seenPatch, recoveredVisit>>

Next == (\E kind \in {"snapshot", "patch"} : Open(kind)) \/
        (\E i \in Chunks : RequestChunk(i)) \/ DeliverChunk \/ DiscardLateReply \/
        (\E check \in Checks \ {"unchecked"} : CheckEnvelope(check)) \/
        Apply \/ SwitchVisit \/ RefreshManifest \/ CloseOld

TypeOK ==
    /\ manifestRevision \in 1..2 /\ lastToken \in 0..2
    /\ transfer \in [token : 0..2, visit : 0..3, link : 0..2,
                    thread : 0..2, panel : 0..2, revision : 0..2,
                    kind : {"snapshot", "patch"}, base : 0..1]
    /\ pending \in [token : 0..2, visit : 0..3, link : 0..2, thread : 0..2,
                   index : Chunks, payloadRevision : 0..2]
    /\ chunks \subseteq Chunks /\ chunkRevision \in [Chunks -> 0..2]
    /\ checked \in Checks /\ clientRevision \in 0..2
    /\ visit \in 1..3 /\ link \in 1..2 /\ thread \in 1..2 /\ chatSeq \in 0..2
    /\ \A flag \in {applied, closedSuccessor, refreshCancelled, sawRefresh,
                    seenSnapshot, seenPatch, recoveredVisit} : flag \in BOOLEAN

FrozenTransferRevision == \A i \in chunks : chunkRevision[i] = transfer.revision
CurrentVisitOwnsApply == applied => CurrentOwner
CompleteBodyBeforeApply == applied => chunks = Chunks
ValidatedBodyBeforeApply == applied => checked = "valid"
OldCloseKeepsSuccessor == ~closedSuccessor
PanelCursorKeepsChatSeq == chatSeq = 0
ManifestKeepsActiveTransfer == ~refreshCancelled

(* Negated reachability witnesses, not eventual-delivery promises. *)
NeverSnapshotApplied == ~seenSnapshot
NeverPatchApplied == ~seenPatch
NeverVisitRecovered == ~recoveredVisit
NeverRefreshCompleted == ~(sawRefresh /\ applied /\ transfer.revision < manifestRevision)

Spec == Init /\ [][Next]_vars
=============================================================================
