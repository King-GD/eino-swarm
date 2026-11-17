------------------------- MODULE StreamingPlayback -------------------------
(***************************************************************************)
(* Streaming PCM ownership, in complete <=250ms scheduling pieces.           *)
(* Byte decoding, browser/OS timing and HTTP authentication are not modeled. *)
(* AcceptHeaders retains the request; CompleteRead schedules without EOF.   *)
(* BeginRead/CompleteRead permit cancellation while a read is in flight.     *)
(* CloseSource ends HTTP/model admission, while playback and its authority   *)
(* observer survive until the scheduled queue drains and audio.cancel runs. *)
(* An expiry after HTTP EOF still aborts the retained playback owner.        *)
(* These are protocol obligations; implementation tests remain necessary.    *)
(***************************************************************************)
EXTENDS Naturals

CONSTANTS Frames, QueueLimit,
          BufferUntilEOF, UnboundedQueue, ReleaseAtHeaders,
          IgnoreCancellation, IgnoreRevocation, KeepQueuedOnStop,
          AcceptLateChunk, ReleaseWatchAtEOF

ASSUME /\ QueueLimit \in Nat \ {0}
       /\ Frames \in Nat /\ Frames > QueueLimit
       /\ BufferUntilEOF \in BOOLEAN /\ UnboundedQueue \in BOOLEAN
       /\ ReleaseAtHeaders \in BOOLEAN /\ IgnoreCancellation \in BOOLEAN
       /\ IgnoreRevocation \in BOOLEAN /\ KeepQueuedOnStop \in BOOLEAN
       /\ AcceptLateChunk \in BOOLEAN /\ ReleaseWatchAtEOF \in BOOLEAN

VARIABLE s
vars == <<s>>
Live == s.phase \in {"headers", "consuming", "draining"}
Terminal == s.phase \in {"done", "cancelled", "revoked"}

Init == s = [phase |-> "headers", sourceOpen |-> TRUE,
             requestHeld |-> TRUE, authorityWatch |-> TRUE,
             playbackHeld |-> TRUE, readPending |-> FALSE,
             eof |-> FALSE, received |-> 0, buffered |-> 0,
             queued |-> 0, played |-> 0, discarded |-> 0,
             firstScheduledBeforeEOF |-> FALSE,
             authorityCurrent |-> TRUE, tailRevocation |-> FALSE]

AcceptHeaders ==
    /\ s.phase = "headers"
    /\ s' = [s EXCEPT !.phase = "consuming",
             !.requestHeld = ~ReleaseAtHeaders,
             !.authorityWatch = ~ReleaseAtHeaders]

(* A consumer requests one more complete piece only when it can schedule it.
   BufferUntilEOF reproduces the prior complete-Blob buffering approach.
   UnboundedQueue removes only playback backpressure, not the finite input. *)
BeginRead ==
    /\ s.phase = "consuming" /\ ~s.eof /\ ~s.readPending
    /\ s.received < Frames
    /\ BufferUntilEOF \/ UnboundedQueue \/ s.queued < QueueLimit
    /\ s' = [s EXCEPT !.readPending = TRUE]

CompleteRead ==
    /\ s.readPending
    /\ s.phase = "consuming" \/ (AcceptLateChunk /\ s.phase \in {"cancelled", "revoked"})
    /\ s' = [s EXCEPT !.readPending = FALSE,
             !.received = @ + 1,
             !.buffered = IF BufferUntilEOF THEN @ + 1 ELSE @,
             !.queued = IF BufferUntilEOF THEN @ ELSE @ + 1,
             !.firstScheduledBeforeEOF = @ \/ (~BufferUntilEOF /\ ~s.eof)]

ObserveEOF ==
    /\ s.phase = "consuming" /\ ~s.eof /\ ~s.readPending
    /\ s.received = Frames
    /\ s' = [s EXCEPT !.eof = TRUE]

ScheduleBuffered ==
    /\ s.phase = "consuming" /\ s.eof /\ s.buffered > 0
    /\ UnboundedQueue \/ s.queued < QueueLimit
    /\ s' = [s EXCEPT !.buffered = @ - 1, !.queued = @ + 1]

(* HTTP EOF/Body.Close releases upstream model admission. The mobile authority
   watcher remains attached to audio.signal until explicit audio.cancel after
   playback drain. ReleaseWatchAtEOF reproduces the premature watcher release. *)
CloseSource ==
    /\ s.phase = "consuming" /\ s.eof /\ s.buffered = 0
    /\ s' = [s EXCEPT !.phase = "draining", !.sourceOpen = FALSE,
             !.requestHeld = FALSE,
             !.authorityWatch = IF ReleaseWatchAtEOF THEN FALSE ELSE @]

(* A scheduled source's ended event releases one queue slot. *)
PlayFrame ==
    /\ s.phase \in {"consuming", "draining"} /\ s.queued > 0
    /\ s' = [s EXCEPT !.queued = @ - 1, !.played = @ + 1]

Finish ==
    /\ s.phase = "draining" /\ s.queued = 0
    /\ s' = [s EXCEPT !.phase = "done", !.playbackHeld = FALSE,
             !.authorityWatch = FALSE]

(* Propagated cancellation is one abstract cleanup action: reader.cancel /
   request cancellation, stop+disconnect every queued source, and release.
   The read callback itself can still arrive; it must be discarded below. *)
Stop(reason, ignore) ==
    /\ Live
    /\ s' = [s EXCEPT !.phase = reason,
             !.authorityCurrent = IF reason = "revoked" THEN FALSE ELSE @,
             !.tailRevocation = @ \/ (reason = "revoked" /\ s.phase = "draining" /\ s.queued > 0),
             !.sourceOpen = IF ignore THEN @ ELSE FALSE,
             !.requestHeld = IF ignore THEN @ ELSE FALSE,
             !.authorityWatch = IF ignore THEN @ ELSE FALSE,
             !.playbackHeld = IF ignore THEN @ ELSE FALSE,
             !.queued = IF ignore \/ KeepQueuedOnStop THEN @ ELSE 0,
             !.buffered = IF ignore \/ KeepQueuedOnStop THEN @ ELSE 0,
             !.discarded = IF ignore \/ KeepQueuedOnStop THEN @
                            ELSE @ + s.queued + s.buffered]
Cancel == Stop("cancelled", IgnoreCancellation)
(* Authority expiry is independent of HTTP. Without the retained watcher no
   abort reaches audio.signal, so no global account/refusal event is invented. *)
Revoke ==
    /\ Live /\ s.authorityCurrent
    /\ IF s.authorityWatch THEN Stop("revoked", IgnoreRevocation)
       ELSE s' = [s EXCEPT !.authorityCurrent = FALSE,
                  !.tailRevocation = @ \/ (s.phase = "draining" /\ s.queued > 0)]

DiscardLateRead ==
    /\ s.phase \in {"cancelled", "revoked"} /\ s.readPending
    /\ s' = [s EXCEPT !.readPending = FALSE]

Next == AcceptHeaders \/ BeginRead \/ CompleteRead \/ ObserveEOF
        \/ ScheduleBuffered \/ CloseSource \/ PlayFrame \/ Finish
        \/ Cancel \/ Revoke \/ DiscardLateRead
Spec == Init /\ [][Next]_vars

(* Fairness is optional, only for the separate termination check. It assumes
   a successful finite producer and delivered cleanup/events, not real-time
   bounds or an upstream that never responds. *)
ProgressSpec == Spec /\ WF_vars(AcceptHeaders) /\ WF_vars(BeginRead)
                /\ WF_vars(CompleteRead) /\ WF_vars(ObserveEOF)
                /\ WF_vars(ScheduleBuffered) /\ WF_vars(CloseSource)
                /\ WF_vars(PlayFrame) /\ WF_vars(Finish)
                /\ WF_vars(DiscardLateRead)
EventuallySettled == <>Terminal

TypeOK ==
    /\ s.phase \in {"headers", "consuming", "draining", "done", "cancelled", "revoked"}
    /\ \A flag \in {s.sourceOpen, s.requestHeld, s.authorityWatch,
                     s.playbackHeld, s.readPending, s.eof,
                     s.firstScheduledBeforeEOF, s.authorityCurrent,
                     s.tailRevocation} : flag \in BOOLEAN
    /\ \A count \in {s.received, s.buffered, s.queued, s.played, s.discarded} : count \in 0..Frames
QueueBounded == s.queued <= QueueLimit
FirstChunkStartsBeforeEOF ==
    (Live /\ s.received > 0 /\ ~s.eof) => s.firstScheduledBeforeEOF
SourceOwnsLease == s.sourceOpen => s.requestHeld /\ s.authorityWatch
PlaybackOwnsQueue == s.queued > 0 => s.playbackHeld
TailRetainsAuthority == (s.phase = "draining" /\ s.queued > 0) => s.authorityWatch
TailRevocationClearsQueue ==
    s.tailRevocation => s.queued = 0 /\ ~s.playbackHeld /\ ~s.authorityWatch
CancelledClosesSource ==
    s.phase = "cancelled" => ~s.sourceOpen /\ ~s.requestHeld /\ ~s.authorityWatch /\ ~s.playbackHeld
RevokedClosesSource ==
    s.phase = "revoked" => ~s.sourceOpen /\ ~s.requestHeld /\ ~s.authorityWatch /\ ~s.playbackHeld
TerminalQueueIsEmpty == Terminal => s.queued = 0 /\ s.buffered = 0
FrameConservation == s.received = s.buffered + s.queued + s.played + s.discarded
FinishedAfterDrain ==
    s.phase = "done" => s.eof /\ s.played = Frames /\ ~s.sourceOpen /\ ~s.playbackHeld

(* Each negative invariant MUST have a counterexample under the correct
   design; these are non-vacuity witnesses, never safety requirements. *)
NeverEarlyPlayback == ~(s.firstScheduledBeforeEOF /\ ~s.eof /\ s.queued > 0)
NeverBackpressure == ~(s.queued = QueueLimit /\ ~s.eof /\ s.received < Frames /\ ~s.readPending)
NeverCancelledPendingRead == ~(s.phase = "cancelled" /\ s.readPending /\ s.received > 0)
NeverRevokedQueuedAudio == ~(s.phase = "revoked" /\ s.discarded > 0)
NeverDrainAfterClose == ~(s.phase = "draining" /\ s.queued > 0 /\ ~s.requestHeld /\ s.playbackHeld)
NeverRevokedQueuedTail == ~(s.tailRevocation /\ s.phase = "revoked" /\ s.discarded > 0)
NeverFinished == s.phase # "done"
=============================================================================
