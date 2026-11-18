------------------------- MODULE ModelModeAdmission -------------------------
EXTENDS Naturals

CONSTANTS
    SampleThenCommit,
    DropBoundCatalog,
    RewriteRecoveredModel,
    RecoverBeforeDiscovery,
    IgnoreRevocation

(* active is the short source check, running is the bound HTTP request.
   Typed calls that retain the gate are abstracted as remaining active. *)
VARIABLES active, running, held, sampled, changed,
          catalog, authorized, refreshed, restarted, binding
retained == <<catalog, authorized, refreshed, restarted, binding>>
vars == <<active, running, held, sampled, changed,
          catalog, authorized, refreshed, restarted, binding>>
Init == /\ active = 0 /\ running = FALSE /\ held = FALSE /\ sampled = FALSE /\ changed = FALSE
        /\ catalog = TRUE /\ authorized = TRUE /\ refreshed = FALSE
        /\ restarted = FALSE /\ binding = "original"
Start == /\ ~held /\ active = 0 /\ ~running /\ ~changed
         /\ active' = 1 /\ UNCHANGED <<running, held, sampled, changed, retained>>
Bind == /\ active = 1 /\ active' = 0 /\ running' = TRUE
        /\ UNCHANGED <<held, sampled, changed, retained>>
Sample == /\ active = 0 /\ ~changed /\ ~sampled
          /\ sampled' = TRUE /\ UNCHANGED <<active, running, held, changed, retained>>
Hold == /\ ~SampleThenCommit /\ active = 0 /\ ~held /\ ~changed
        /\ held' = TRUE /\ UNCHANGED <<active, running, sampled, changed, retained>>
Commit == /\ ~changed
          /\ IF SampleThenCommit THEN sampled ELSE held /\ active = 0
          /\ changed' = TRUE /\ UNCHANGED <<active, running, held, sampled, retained>>
(* Discovery is independent of picker visibility. A mode save cannot revoke
   an authorized source needed by the durable turn/worker binding. *)
Refresh == /\ changed /\ ~refreshed
           /\ refreshed' = TRUE /\ catalog' = (authorized /\ ~DropBoundCatalog)
           /\ UNCHANGED <<active, running, held, sampled, changed, authorized, restarted, binding>>
Restart == /\ running /\ (refreshed \/ RecoverBeforeDiscovery) /\ authorized /\ ~restarted
           /\ restarted' = TRUE
           /\ binding' = IF RewriteRecoveredModel THEN "preset" ELSE binding
           /\ UNCHANGED <<active, running, held, sampled, changed, catalog, authorized, refreshed>>
Revoke == /\ authorized /\ authorized' = FALSE
          /\ catalog' = IF IgnoreRevocation THEN catalog ELSE FALSE
          /\ UNCHANGED <<active, running, held, sampled, changed, refreshed, restarted, binding>>
Next == Start \/ Bind \/ Sample \/ Hold \/ Commit \/ Refresh \/ Restart \/ Revoke
Spec == Init /\ [][Next]_vars
TypeOK == /\ active \in 0..1 /\ held \in BOOLEAN
          /\ sampled \in BOOLEAN /\ changed \in BOOLEAN /\ running \in BOOLEAN
          /\ catalog \in BOOLEAN /\ authorized \in BOOLEAN /\ refreshed \in BOOLEAN
          /\ restarted \in BOOLEAN /\ binding \in {"original", "preset"}
SwitchIsIdle == changed => active = 0
BoundCatalogSurvives == (running /\ authorized /\ refreshed) => catalog
RecoveryKeepsBinding == binding = "original"
RecoveryWaitsForDiscovery == restarted => refreshed
RevocationClosesSource == ~authorized => ~catalog
NeverSwitchWithBoundRequest == ~(changed /\ running)
NeverRecoverAfterSwitch == ~restarted
=============================================================================
