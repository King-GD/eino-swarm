------------------------- MODULE ModelModeAdmission -------------------------
EXTENDS Naturals

CONSTANTS
    SampleThenCommit

VARIABLES active, held, sampled, changed
vars == <<active, held, sampled, changed>>
Init == /\ active = 0 /\ held = FALSE /\ sampled = FALSE /\ changed = FALSE
Start == /\ ~held /\ active = 0 /\ ~changed
         /\ active' = 1 /\ UNCHANGED <<held, sampled, changed>>
Sample == /\ active = 0 /\ ~changed /\ ~sampled
          /\ sampled' = TRUE /\ UNCHANGED <<active, held, changed>>
Hold == /\ ~SampleThenCommit /\ active = 0 /\ ~held /\ ~changed
        /\ held' = TRUE /\ UNCHANGED <<active, sampled, changed>>
Commit == /\ ~changed
          /\ IF SampleThenCommit THEN sampled ELSE held /\ active = 0
          /\ changed' = TRUE /\ UNCHANGED <<active, held, sampled>>
Next == Start \/ Sample \/ Hold \/ Commit
Spec == Init /\ [][Next]_vars
TypeOK == /\ active \in 0..1 /\ held \in BOOLEAN
          /\ sampled \in BOOLEAN /\ changed \in BOOLEAN
SwitchIsIdle == changed => active = 0
NeverSwitch == ~changed
=============================================================================
