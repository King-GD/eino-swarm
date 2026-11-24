------------------------ MODULE DistillationProgress ------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS FinishAfterFirstBatch, AdvanceBeforeAcceptance, ForgetCursorOnResume
Batches == 1..2
VARIABLES state, cursor, accepted, resumed
vars == <<state, cursor, accepted, resumed>>

Init == /\ state = "running"
        /\ cursor = 0
        /\ accepted = {}
        /\ resumed = FALSE

Commit == /\ state = "running"
          /\ cursor < 2
          /\ cursor' = cursor + 1
          /\ accepted' = accepted \cup {cursor'}
          /\ UNCHANGED <<state, resumed>>

EarlyAdvance == /\ AdvanceBeforeAcceptance
                /\ state = "running"
                /\ cursor < 2
                /\ cursor' = cursor + 1
                /\ UNCHANGED <<state, accepted, resumed>>

Interrupt == /\ state = "running"
             /\ state' = "interrupted"
             /\ UNCHANGED <<cursor, accepted, resumed>>

Resume == /\ state = "interrupted"
          /\ state' = "running"
          /\ resumed' = (resumed \/ cursor > 0)
          /\ cursor' = IF ForgetCursorOnResume THEN 0 ELSE cursor
          /\ UNCHANGED accepted

Finish == /\ state = "running"
          /\ (cursor = 2 \/ (FinishAfterFirstBatch /\ cursor = 1))
          /\ state' = "completed"
          /\ UNCHANGED <<cursor, accepted, resumed>>

Spec == Init /\ [][Commit \/ EarlyAdvance \/ Interrupt \/ Resume \/ Finish]_vars

TypeOK == /\ state \in {"running", "interrupted", "completed"}
          /\ cursor \in 0..2
          /\ accepted \subseteq Batches
          /\ resumed \in BOOLEAN
AcceptBeforeAdvance == 1..cursor \subseteq accepted
KeepCheckpoint == accepted \subseteq 1..cursor
CompleteCoversScope == state = "completed" => accepted = Batches
NeverCompleteAfterResume == ~(resumed /\ state = "completed")
=============================================================================
