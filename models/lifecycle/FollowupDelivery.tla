-------------------------- MODULE FollowupDelivery --------------------------
EXTENDS Naturals

(* A delayed client targets turn 1 and the second queued message. Finishing
   can race with delivery. SplitInterrupt models the old second RPC, which
   accepts only a conversation ID and can preempt its successor. *)
CONSTANT SplitInterrupt
VARIABLES active, queue, delivered, interrupt, pending
vars == <<active, queue, delivered, interrupt, pending>>

Init == /\ active = 1 /\ queue = {"first", "selected"}
        /\ delivered = 0 /\ interrupt = 0 /\ pending = FALSE

Finish == /\ active = 1 /\ active' = 2
          /\ UNCHANGED <<queue, delivered, interrupt, pending>>

Insert == /\ active = 1 /\ "selected" \in queue
          /\ queue' = queue \ {"selected"} /\ delivered' = active
          /\ UNCHANGED <<active, pending, interrupt>>

InsertNow == /\ active = 1 /\ "selected" \in queue
             /\ queue' = queue \ {"selected"} /\ delivered' = active
             /\ IF SplitInterrupt
                   THEN /\ pending' = TRUE /\ UNCHANGED interrupt
                   ELSE /\ interrupt' = active /\ UNCHANGED pending
             /\ UNCHANGED active

DelayedInterrupt == /\ SplitInterrupt /\ pending
                    /\ interrupt' = active /\ pending' = FALSE
                    /\ UNCHANGED <<active, queue, delivered>>

Next == Finish \/ Insert \/ InsertNow \/ DelayedInterrupt
Spec == Init /\ [][Next]_vars

TypeOK == /\ active \in 1..2 /\ queue \subseteq {"first", "selected"}
          /\ delivered \in 0..1 /\ interrupt \in 0..2 /\ pending \in BOOLEAN
InterruptMatchesDelivery == interrupt # 0 => interrupt = delivered
NeverInsertNow == interrupt = 0
NeverInsertWithoutInterrupt == delivered = 0 \/ interrupt # 0
NeverKeepOtherRow == delivered = 0 \/ "first" \notin queue
=============================================================================
