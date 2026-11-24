--------------------------- MODULE PanelCloseOrdering ------------------------
EXTENDS Naturals

CONSTANTS CancelBeforeCommit, AckBeforeCancel, CancelSibling,
          AcceptLateWrite, CloseWithoutDeletion, ForgetCloseOnRestart,
          AcceptForeignClose

(* P and Q have distinct running source contexts. Only P receives a close.
   SQLite commits the marker, source status, revision and deletion together.
   A cancelled context can still own an unwinding producer.
   siblingCancelled tracks a cancellation attributed to close, not process exit. *)
VARIABLES disk, source, siblingCancelled, attempt, failed, publication, restartFrom,
          foreign, unsafeCancel, lateWrite
restarted == restartFrom # "none"
vars == <<disk, source, siblingCancelled, attempt, failed, publication, restartFrom,
          foreign, unsafeCancel, lateWrite>>

Init ==
    /\ disk = [closed |-> FALSE, status |-> "running", revision |-> 1,
               observation |-> FALSE, deletion |-> FALSE]
    /\ source = "running" /\ siblingCancelled = FALSE
    /\ attempt = "idle" /\ failed = FALSE
    /\ publication = [phase |-> "idle", base |-> 0]
    /\ restartFrom = "none" /\ foreign = FALSE
    /\ unsafeCancel = FALSE /\ lateWrite = FALSE

CloseRecord == [disk EXCEPT !.closed = TRUE, !.status = "interrupted",
                            !.revision = @ + 1, !.deletion = ~CloseWithoutDeletion]

Request ==
    /\ attempt = "idle" /\ ~disk.closed /\ ~restarted
    /\ attempt' = "pending"
    /\ UNCHANGED <<disk, source, siblingCancelled, failed, publication, restartFrom,
                   foreign, unsafeCancel, lateWrite>>

ForeignRequest ==
    /\ ~foreign /\ attempt = "idle" /\ ~disk.closed /\ ~restarted
    /\ foreign' = TRUE
    /\ disk' = IF AcceptForeignClose THEN CloseRecord ELSE disk
    /\ UNCHANGED <<source, siblingCancelled, attempt, failed, publication, restartFrom,
                   unsafeCancel, lateWrite>>

WriteFails ==
    /\ attempt = "pending" /\ ~failed
    /\ attempt' = "idle" /\ failed' = TRUE
    /\ source' = IF CancelBeforeCommit THEN "cancelled" ELSE source
    /\ unsafeCancel' = (unsafeCancel \/ source' # source)
    /\ UNCHANGED <<disk, siblingCancelled, publication, restartFrom, foreign, lateWrite>>

CommitClose ==
    /\ attempt = "pending"
    /\ attempt' = "committed" /\ disk' = CloseRecord
    /\ UNCHANGED <<source, siblingCancelled, failed, publication, restartFrom,
                   foreign, unsafeCancel, lateWrite>>

CancelOwned ==
    /\ attempt \in {"committed", "acknowledged"} /\ source = "running"
    /\ source' = "cancelled"
    /\ siblingCancelled' = CancelSibling
    /\ UNCHANGED <<disk, attempt, failed, publication, restartFrom,
                   foreign, unsafeCancel, lateWrite>>

Unwind ==
    /\ source = "cancelled" /\ source' = "exited"
    /\ UNCHANGED <<disk, siblingCancelled, attempt, failed, publication, restartFrom,
                   foreign, unsafeCancel, lateWrite>>

Ack ==
    /\ attempt = "committed" /\ ~restarted
    /\ (source # "running" \/ AckBeforeCancel)
    /\ attempt' = "acknowledged"
    /\ UNCHANGED <<disk, source, siblingCancelled, failed, publication, restartFrom,
                   foreign, unsafeCancel, lateWrite>>

Restart ==
    /\ attempt \in {"committed", "acknowledged"} /\ ~restarted
    /\ restartFrom' = source /\ source' = "exited"
    /\ disk' = IF ForgetCloseOnRestart THEN [disk EXCEPT !.closed = FALSE] ELSE disk
    /\ UNCHANGED <<siblingCancelled, attempt, failed, publication, foreign,
                   unsafeCancel, lateWrite>>

PreparePublish ==
    /\ publication.phase = "idle"
    /\ publication' = [phase |-> "pending", base |-> disk.revision]
    /\ UNCHANGED <<disk, source, siblingCancelled, attempt, failed, restartFrom, foreign,
                   unsafeCancel, lateWrite>>

ResolvePublish ==
    /\ publication.phase = "pending"
    /\ LET accept == (~disk.closed \/ AcceptLateWrite) /\ publication.base = disk.revision
       IN /\ publication' = [publication EXCEPT !.phase = IF accept THEN "accepted" ELSE "rejected"]
          /\ disk' = IF accept THEN [disk EXCEPT !.observation = TRUE, !.revision = @ + 1] ELSE disk
          /\ lateWrite' = (lateWrite \/ (disk.closed /\ accept))
    /\ UNCHANGED <<source, siblingCancelled, attempt, failed, restartFrom, foreign, unsafeCancel>>

Next == Request \/ ForeignRequest \/ WriteFails \/ CommitClose \/ CancelOwned \/
        Unwind \/ Ack \/ Restart \/ PreparePublish \/ ResolvePublish
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ disk \in [closed : BOOLEAN, status : {"running", "interrupted"},
                 revision : 1..3, observation : BOOLEAN, deletion : BOOLEAN]
    /\ attempt \in {"idle", "pending", "committed", "acknowledged"}
    /\ source \in {"running", "cancelled", "exited"} /\ siblingCancelled \in BOOLEAN
    /\ publication \in [phase : {"idle", "pending", "accepted", "rejected"}, base : 0..3]
    /\ failed \in BOOLEAN /\ restartFrom \in {"none", "running", "cancelled", "exited"} /\ foreign \in BOOLEAN
    /\ unsafeCancel \in BOOLEAN /\ lateWrite \in BOOLEAN

FailedCloseDoesNotCancel == ~unsafeCancel
CloseRecordsDeletion == disk.closed => disk.deletion /\ disk.status = "interrupted"
CloseCancelsOnlyOwner == ~siblingCancelled
AckAfterCancellation == attempt = "acknowledged" => source # "running"
NoLatePublication == ~lateWrite
CloseSurvivesRestart == restarted => disk.closed
ForeignDoesNotClose == disk.closed => attempt \in {"committed", "acknowledged"}

NeverRetryAfterWriteFailure == ~(failed /\ attempt = "acknowledged")
NeverAckBeforeJoin == ~(attempt = "acknowledged" /\ source = "cancelled")
NeverCrashBeforeCancel == ~(restartFrom = "running" /\ attempt = "committed" /\ source = "exited")
NeverPublishBeforeClose == ~(disk.closed /\ disk.observation)
NeverRejectCapturedPublish == ~(disk.closed /\ publication.base = 1 /\ publication.phase = "rejected")
=============================================================================
