---------------------------- MODULE MCPCallRecovery ----------------------------
EXTENDS Naturals

(* "expired" is a protocol rejection before execution. A Streamable HTTP
   call's local "deadline" leaves the effect unknown and the session usable
   by independent work. Other failures reserve the task's admission gate.
   A manager request already admitted receives cancellation on block. *)
CONSTANTS
    RetryUncertain,
    IgnoreBlock,
    RenewAgain,
    LeaveManagerRunning,
    BlockDeadline

VARIABLES phase, attempts, first, result, nextWork, cancelManager
vars == <<phase, attempts, first, result, nextWork, cancelManager>>

Outcomes == {"ok", "expired", "deadline", "unknown"}
TypeOK ==
    /\ phase \in {"send", "resolve", "blocked", "done"}
    /\ attempts \in 0..4
    /\ first \in Outcomes \cup {"none"}
    /\ result \in Outcomes \cup {"none"}
    /\ nextWork \in BOOLEAN
    /\ cancelManager \in BOOLEAN
VerifiedBeforeRetry == attempts < 2 \/ first = "expired"
OneReplacement == attempts <= 2
NoWorkAfterBlock == phase /= "blocked" \/ ~nextWork
ManagerCancellationRequested == phase /= "blocked" \/ cancelManager
DeadlineKeepsAdmission == result /= "deadline" \/ phase # "blocked"
NoWorkAfterDeadline == ~(result = "deadline" /\ nextWork)
NeverRecovered == ~(phase = "done" /\ attempts = 2 /\ result = "ok")
NeverBlocked == phase /= "blocked"

Init ==
    /\ phase = "send"
    /\ attempts = 0
    /\ first = "none"
    /\ result = "none"
    /\ nextWork = FALSE
    /\ cancelManager = FALSE

Send(outcome) ==
    /\ phase = "send"
    /\ attempts < 4
    /\ attempts' = attempts + 1
    /\ first' = IF attempts = 0 THEN outcome ELSE first
    /\ result' = outcome
    /\ phase' = "resolve"
    /\ UNCHANGED <<nextWork, cancelManager>>

Decide ==
    /\ phase = "resolve"
    /\ phase' = IF result = "ok" THEN "done"
       ELSE IF (result = "expired" \/ RetryUncertain) /\ (attempts = 1 \/ RenewAgain)
            THEN "send"
            ELSE IF result = "deadline" /\ ~BlockDeadline THEN "done"
                 ELSE "blocked"
    /\ cancelManager' = (phase' = "blocked" /\ ~LeaveManagerRunning)
    /\ UNCHANGED <<attempts, first, result, nextWork>>

OtherWork ==
    /\ phase = "done" \/ (phase = "blocked" /\ IgnoreBlock)
    /\ nextWork' = TRUE
    /\ UNCHANGED <<phase, attempts, first, result, cancelManager>>

Next == (\E outcome \in Outcomes: Send(outcome)) \/ Decide \/ OtherWork
Spec == Init /\ [][Next]_vars
=============================================================================
