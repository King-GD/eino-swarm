-------------------------- MODULE PanelCloseDelivery --------------------------
EXTENDS Naturals

CONSTANTS IgnoreCloseAck, ApplyOldPayload, ApplyOldAck

(* One instance, two visits and one in-flight payload. The payload stands for
   a PC snapshot or a phone manifest/body captured before the close. A fresh
   authoritative read after reconnect sees the durable marker. *)
VARIABLES hostClosed, visit, shown, localClosed, close, reply, revived, staleAck
vars == <<hostClosed, visit, shown, localClosed, close, reply, revived, staleAck>>

Init ==
    /\ hostClosed = FALSE /\ visit = 1 /\ shown = TRUE /\ localClosed = FALSE
    /\ close = [phase |-> "idle", visit |-> 0]
    /\ reply = [phase |-> "idle", visit |-> 0, visible |-> FALSE]
    /\ revived = FALSE /\ staleAck = FALSE

Capture ==
    /\ reply.phase = "idle"
    /\ reply' = [phase |-> "inflight", visit |-> visit, visible |-> ~hostClosed]
    /\ UNCHANGED <<hostClosed, visit, shown, localClosed, close, revived, staleAck>>

SendClose ==
    /\ close.phase = "idle" /\ shown
    /\ close' = [phase |-> "pending", visit |-> visit]
    /\ UNCHANGED <<hostClosed, visit, shown, localClosed, reply, revived, staleAck>>

CommitClose ==
    /\ close.phase = "pending"
    /\ hostClosed' = TRUE /\ close' = [close EXCEPT !.phase = "committed"]
    /\ UNCHANGED <<visit, shown, localClosed, reply, revived, staleAck>>

Ack ==
    /\ close.phase = "committed"
    /\ close' = [close EXCEPT !.phase = "returned"]
    /\ LET own == (close.visit = visit \/ ApplyOldAck) /\ ~IgnoreCloseAck
       IN /\ shown' = IF own THEN FALSE ELSE shown
          /\ localClosed' = (localClosed \/ own)
          /\ staleAck' = (staleAck \/ (own /\ close.visit # visit))
    /\ UNCHANGED <<hostClosed, visit, reply, revived>>

Receive ==
    /\ reply.phase = "inflight"
    /\ reply' = [reply EXCEPT !.phase = "returned"]
    /\ shown' = IF reply.visit # visit THEN shown
                 ELSE reply.visible /\ (~localClosed \/ ApplyOldPayload)
    /\ revived' = (revived \/ (localClosed /\ shown'))
    /\ UNCHANGED <<hostClosed, visit, localClosed, close, staleAck>>

Reconnect ==
    /\ visit = 1 /\ visit' = 2
    /\ shown' = ~hostClosed /\ localClosed' = FALSE
    /\ UNCHANGED <<hostClosed, close, reply, revived, staleAck>>

Next == Capture \/ SendClose \/ CommitClose \/ Ack \/ Receive \/ Reconnect
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ hostClosed \in BOOLEAN /\ shown \in BOOLEAN /\ localClosed \in BOOLEAN
    /\ revived \in BOOLEAN /\ staleAck \in BOOLEAN /\ visit \in 1..2
    /\ close \in [phase : {"idle", "pending", "committed", "returned"}, visit : 0..2]
    /\ reply \in [phase : {"idle", "inflight", "returned"}, visit : 0..2, visible : BOOLEAN]

AckHidesCurrentInstance == (close.phase = "returned" /\ close.visit = visit) => localClosed /\ ~shown
OldPayloadCannotRevive == ~revived
OldAckCannotAffectCurrentVisit == ~staleAck

NeverCrossedAck == ~(localClosed /\ reply.phase = "inflight" /\ reply.visible /\ reply.visit = visit)
NeverReturnedOldAck == ~(visit = 2 /\ close.visit = 1 /\ close.phase = "returned")
NeverReconnectedClosed == ~(hostClosed /\ visit = 2 /\ ~shown /\ close.phase = "committed")
=============================================================================
