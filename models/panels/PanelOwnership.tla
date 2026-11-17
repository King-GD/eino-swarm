---------------------------- MODULE PanelOwnership ----------------------------
EXTENDS Naturals, FiniteSets

CONSTANTS
    StealRunningSource,
    AbortAllRecovery

(* A live source and a data author have different lifetimes. B may attempt a
   write while A is still doing external work. The bounded recovery pass then
   visits one malformed panel thread followed by an independent healthy one. *)
VARIABLES owner, sourceStatus, externalA, attemptedB, rejectedB, wroteA,
          recoveryStep, badReported, goodRecovered

vars == <<owner, sourceStatus, externalA, attemptedB, rejectedB, wroteA,
          recoveryStep, badReported, goodRecovered>>
sourceVars == <<owner, sourceStatus, externalA, attemptedB, rejectedB, wroteA>>
recoveryVars == <<recoveryStep, badReported, goodRecovered>>

Init ==
    /\ owner = "none" /\ sourceStatus = "idle" /\ externalA = FALSE
    /\ attemptedB = FALSE /\ rejectedB = FALSE /\ wroteA = FALSE
    /\ recoveryStep = "bad" /\ badReported = FALSE /\ goodRecovered = FALSE

StartA ==
    /\ owner = "none" /\ sourceStatus = "idle" /\ ~externalA
    /\ owner' = "A" /\ sourceStatus' = "running" /\ externalA' = TRUE
    /\ UNCHANGED <<attemptedB, rejectedB, wroteA, recoveryVars>>

PublishA ==
    /\ externalA /\ owner = "A" /\ sourceStatus = "running" /\ ~wroteA
    /\ wroteA' = TRUE
    /\ UNCHANGED <<owner, sourceStatus, externalA, attemptedB, rejectedB, recoveryVars>>

PublishB ==
    /\ externalA /\ ~attemptedB
    /\ attemptedB' = TRUE
    /\ rejectedB' = ~StealRunningSource
    /\ owner' = IF StealRunningSource THEN "B" ELSE owner
    /\ sourceStatus' = IF StealRunningSource THEN "idle" ELSE sourceStatus
    /\ UNCHANGED <<externalA, wroteA, recoveryVars>>

FinishA ==
    /\ externalA /\ owner = "A" /\ sourceStatus = "running"
    /\ externalA' = FALSE /\ sourceStatus' = "finished"
    /\ UNCHANGED <<owner, attemptedB, rejectedB, wroteA, recoveryVars>>

RecoverBad ==
    /\ recoveryStep = "bad"
    /\ badReported' = TRUE
    /\ recoveryStep' = IF AbortAllRecovery THEN "done" ELSE "good"
    /\ UNCHANGED <<sourceVars, goodRecovered>>

RecoverGood ==
    /\ recoveryStep = "good"
    /\ goodRecovered' = TRUE /\ recoveryStep' = "done"
    /\ UNCHANGED <<sourceVars, badReported>>

Next == StartA \/ PublishA \/ PublishB \/ FinishA \/ RecoverBad \/ RecoverGood
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ owner \in {"none", "A", "B"}
    /\ sourceStatus \in {"idle", "running", "finished"}
    /\ externalA \in BOOLEAN /\ attemptedB \in BOOLEAN /\ rejectedB \in BOOLEAN
    /\ wroteA \in BOOLEAN /\ recoveryStep \in {"bad", "good", "done"}
    /\ badReported \in BOOLEAN /\ goodRecovered \in BOOLEAN

RunningOwnerKept == externalA => (owner = "A" /\ sourceStatus = "running")
HealthyThreadsRecover == recoveryStep = "done" => (badReported /\ goodRecovered)

NeverRunningSource == ~externalA
NeverRejectedOtherPublisher == ~rejectedB
NeverHealthyRecovery == ~goodRecovered
=============================================================================
