--------------------- MODULE WorkerCollectionAdmission ---------------------
EXTENDS Naturals

(* One worker-collection wait. The engine stores workers_collection_resumed
   and must withdraw the in-memory waiter before releasing the runtime lock.
   ContinueTurn(false) writes workers_collection_declined only while that
   waiter is still the open pause and the log has no resume receipt.

   ReleaseAdmissionLater restores dropping the lock before withdrawal.
   DeclineSkipsLog restores appending the decline without reading the log.
   Together they store both receipts and the next fold cannot parse the wait.
   StopIgnored restores granting the resume after a durable Stop.

   This is the admission protocol, not worker execution time or the card view. *)

CONSTANTS
    ReleaseAdmissionLater,
    DeclineSkipsLog,
    StopIgnored

VARIABLES
    phase,
    admission,
    resumeStored,
    declineStored,
    stopStored,
    declineRefused,
    resumeRefused,
    live

vars == <<phase, admission, resumeStored, declineStored, stopStored,
          declineRefused, resumeRefused, live>>

Init ==
    /\ phase = "idle"
    /\ admission = "closed"
    /\ resumeStored = FALSE
    /\ declineStored = FALSE
    /\ stopStored = FALSE
    /\ declineRefused = FALSE
    /\ resumeRefused = FALSE
    /\ live = "working"

OpenWait ==
    /\ phase = "idle"
    /\ phase' = "waiting"
    /\ admission' = "open"
    /\ UNCHANGED <<resumeStored, declineStored, stopStored, declineRefused, resumeRefused, live>>

CommitResume ==
    /\ phase = "waiting"
    /\ admission = "open"
    /\ ~resumeStored
    /\ StopIgnored \/ ~stopStored
    /\ phase' = "resumed"
    /\ resumeStored' = TRUE
    /\ admission' = IF ReleaseAdmissionLater THEN "open" ELSE "closed"
    /\ live' = "collecting"
    /\ UNCHANGED <<declineStored, stopStored, declineRefused, resumeRefused>>

ClearAdmission ==
    /\ ReleaseAdmissionLater
    /\ resumeStored
    /\ admission = "open"
    /\ admission' = "closed"
    /\ UNCHANGED <<phase, resumeStored, declineStored, stopStored, declineRefused, resumeRefused, live>>

Stop ==
    /\ phase = "waiting"
    /\ ~stopStored
    /\ ~resumeStored
    /\ stopStored' = TRUE
    /\ live' = "stopped"
    /\ UNCHANGED <<phase, admission, resumeStored, declineStored, declineRefused, resumeRefused>>

RefuseResumeAfterStop ==
    /\ phase = "waiting"
    /\ stopStored
    /\ ~StopIgnored
    /\ ~resumeRefused
    /\ resumeRefused' = TRUE
    /\ UNCHANGED <<phase, admission, resumeStored, declineStored, stopStored, declineRefused, live>>

Decline ==
    /\ admission = "open"
    /\ ~declineStored
    /\ \/ /\ ~resumeStored
          /\ declineStored' = TRUE
          /\ phase' = "declined"
          /\ admission' = "closed"
          /\ live' = "stopped"
          /\ UNCHANGED <<resumeStored, stopStored, declineRefused, resumeRefused>>
       \/ /\ resumeStored
          /\ DeclineSkipsLog
          /\ declineStored' = TRUE
          /\ UNCHANGED <<phase, admission, live, resumeStored, stopStored, declineRefused, resumeRefused>>
       \/ /\ resumeStored
          /\ ~DeclineSkipsLog
          /\ declineRefused' = TRUE
          /\ UNCHANGED <<phase, admission, live, resumeStored, declineStored, stopStored, resumeRefused>>

DeclineAfterClose ==
    /\ admission = "closed"
    /\ resumeStored
    /\ ~declineStored
    /\ ~declineRefused
    /\ declineRefused' = TRUE
    /\ UNCHANGED <<phase, admission, resumeStored, declineStored, stopStored, resumeRefused, live>>

Next ==
    OpenWait \/ CommitResume \/ ClearAdmission \/ Stop \/ RefuseResumeAfterStop
    \/ Decline \/ DeclineAfterClose

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ phase \in {"idle", "waiting", "resumed", "declined"}
    /\ admission \in {"closed", "open"}
    /\ resumeStored \in BOOLEAN
    /\ declineStored \in BOOLEAN
    /\ stopStored \in BOOLEAN
    /\ declineRefused \in BOOLEAN
    /\ resumeRefused \in BOOLEAN
    /\ live \in {"working", "collecting", "stopped"}

AdmissionClosedAfterResume ==
    resumeStored => admission = "closed"

NoDeclineAfterResume ==
    ~(resumeStored /\ declineStored)

StopPrecedesResume ==
    stopStored => ~resumeStored

NeverResumeCommitted ==
    ~resumeStored

NeverDeclineWins ==
    ~(phase = "declined" /\ declineStored /\ ~resumeStored)

NeverLateDeclineRefused ==
    ~(resumeStored /\ declineRefused /\ ~declineStored)

NeverStopBlocksResume ==
    ~(stopStored /\ resumeRefused /\ ~resumeStored)
=============================================================================
