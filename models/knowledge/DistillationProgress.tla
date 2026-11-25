------------------------ MODULE DistillationProgress ------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS FinishAfterFirstBatch, AdvanceBeforeAcceptance, ForgetCursorOnResume,
          CountIncompleteRecord, ForgetSegmentOnResume
Parts == {<<1, 1>>, <<1, 2>>, <<2, 1>>}
VARIABLES state, cursor, segment, accepted, resumed
vars == <<state, cursor, segment, accepted, resumed>>

CheckpointParts == {p \in Parts : p[1] <= cursor \/ (p[1] = cursor + 1 /\ p[2] <= segment)}
FinishesRecord == cursor = 1 \/ segment = 1

Init == /\ state = "running"
        /\ cursor = 0
        /\ segment = 0
        /\ accepted = {}
        /\ resumed = FALSE

Commit == /\ state = "running"
          /\ cursor < 2
          /\ accepted' = accepted \cup {<<cursor + 1, segment + 1>>}
          /\ IF FinishesRecord \/ CountIncompleteRecord
                THEN /\ cursor' = cursor + 1
                     /\ segment' = 0
                ELSE /\ cursor' = cursor
                     /\ segment' = segment + 1
          /\ UNCHANGED <<state, resumed>>

EarlyAdvance == /\ AdvanceBeforeAcceptance
                /\ state = "running"
                /\ cursor < 2
                /\ cursor' = cursor + 1
                /\ segment' = 0
                /\ UNCHANGED <<state, accepted, resumed>>

Interrupt == /\ state = "running"
             /\ state' = "interrupted"
             /\ UNCHANGED <<cursor, segment, accepted, resumed>>

Resume == /\ state = "interrupted"
          /\ state' = "running"
          /\ resumed' = (resumed \/ segment > 0)
          /\ cursor' = IF ForgetCursorOnResume THEN 0 ELSE cursor
          /\ segment' = IF ForgetCursorOnResume \/ ForgetSegmentOnResume THEN 0 ELSE segment
          /\ UNCHANGED accepted

Finish == /\ state = "running"
          /\ (cursor = 2 \/ (FinishAfterFirstBatch /\ cursor = 1))
          /\ state' = "completed"
          /\ UNCHANGED <<cursor, segment, accepted, resumed>>

Spec == Init /\ [][Commit \/ EarlyAdvance \/ Interrupt \/ Resume \/ Finish]_vars

TypeOK == /\ state \in {"running", "interrupted", "completed"}
          /\ cursor \in 0..2
          /\ segment \in 0..1
          /\ accepted \subseteq Parts
          /\ resumed \in BOOLEAN
AcceptBeforeAdvance == CheckpointParts \subseteq accepted
KeepCheckpoint == accepted \subseteq CheckpointParts
CompleteCoversScope == state = "completed" => accepted = Parts
NeverCompleteAfterResume == ~(resumed /\ state = "completed")
=============================================================================
