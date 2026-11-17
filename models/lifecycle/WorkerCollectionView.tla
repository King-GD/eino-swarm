------------------------ MODULE WorkerCollectionView ------------------------
EXTENDS Naturals

(* Desktop collection card versus the turn the composer calls running.
   statusRunning is the server turn flag (composer placeholder and Stop).
   transcriptRunning is the folded transcript flag. A paged tail can set the
   first without the second. A decline is stored while the turn is still
   running; the idle sentence belongs only to the later terminal state.

   GateOnTranscriptRunning restores the card that treated
   confirm.pending && transcriptRunning as the only open pause, so a live
   pause with an idle transcript flag was painted stopped and lost Continue.
   DeclineShownAsIdle restores painting that same idle sentence as soon as
   the decline is stored, while the composer still says the turn is working.

   Peer delivery does not settle the pause. This is the view protocol, not
   Go locking or worker cancellation time. *)

CONSTANTS
    GateOnTranscriptRunning,
    DeclineShownAsIdle

VARIABLES
    statusRunning,
    transcriptRunning,
    pause,
    automatic,
    peerSeen

vars == <<statusRunning, transcriptRunning, pause, automatic, peerSeen>>

View ==
    IF pause = "pending" THEN
        IF statusRunning /\ (~GateOnTranscriptRunning \/ transcriptRunning)
        THEN IF automatic THEN "waiting" ELSE "paused"
        ELSE "stopped"
    ELSE IF pause = "continued" THEN "continued"
    ELSE IF pause = "declined" THEN
        IF statusRunning /\ ~DeclineShownAsIdle THEN "finishing" ELSE "stopped"
    ELSE "hidden"

Init ==
    /\ statusRunning = FALSE
    /\ transcriptRunning = FALSE
    /\ pause = "none"
    /\ automatic = FALSE
    /\ peerSeen = FALSE

Start ==
    /\ ~statusRunning
    /\ pause = "none"
    /\ statusRunning' = TRUE
    /\ transcriptRunning' = TRUE
    /\ UNCHANGED <<pause, automatic, peerSeen>>

\* A collection pause event marks the server turn running. The folded page
\* may not contain the earlier start, so the transcript flag stays false.
EnterPause ==
    /\ pause = "none"
    /\ pause' = "pending"
    /\ automatic' \in BOOLEAN
    /\ UNCHANGED peerSeen
    /\ \/ /\ statusRunning
          /\ UNCHANGED <<statusRunning, transcriptRunning>>
       \/ /\ ~statusRunning
          /\ statusRunning' = TRUE
          /\ transcriptRunning' = FALSE

SyncTranscript ==
    /\ transcriptRunning # statusRunning
    /\ transcriptRunning' = statusRunning
    /\ UNCHANGED <<statusRunning, pause, automatic, peerSeen>>

Decline ==
    /\ pause = "pending"
    /\ pause' = "declined"
    /\ UNCHANGED <<statusRunning, transcriptRunning, automatic, peerSeen>>

Continue ==
    /\ pause = "pending"
    /\ pause' = "continued"
    /\ UNCHANGED <<statusRunning, transcriptRunning, automatic, peerSeen>>

Terminal ==
    /\ statusRunning
    /\ statusRunning' = FALSE
    /\ transcriptRunning' = FALSE
    /\ UNCHANGED <<pause, automatic, peerSeen>>

Peer ==
    /\ statusRunning
    /\ ~peerSeen
    /\ peerSeen' = TRUE
    /\ UNCHANGED <<statusRunning, transcriptRunning, pause, automatic>>

Next ==
    Start \/ EnterPause \/ SyncTranscript \/ Decline \/ Continue \/ Terminal \/ Peer

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ statusRunning \in BOOLEAN
    /\ transcriptRunning \in BOOLEAN
    /\ pause \in {"none", "pending", "declined", "continued"}
    /\ automatic \in BOOLEAN
    /\ peerSeen \in BOOLEAN
    /\ View \in {"hidden", "waiting", "paused", "continued", "finishing", "stopped"}

RunningPauseStaysActionable ==
    statusRunning /\ pause = "pending" => View \in {"waiting", "paused"}

StoppedViewIsIdle ==
    View = "stopped" => ~statusRunning

DeclineWhileRunningIsClosing ==
    statusRunning /\ pause = "declined" => View = "finishing"

NeverDivergentPause ==
    ~(statusRunning /\ ~transcriptRunning /\ pause = "pending" /\ View \in {"waiting", "paused"})

NeverClosing ==
    ~(peerSeen /\ statusRunning /\ pause = "declined" /\ View = "finishing")

NeverIdleStop ==
    ~(pause = "declined" /\ ~statusRunning /\ ~transcriptRunning /\ View = "stopped")
=============================================================================
