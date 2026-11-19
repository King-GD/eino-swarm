---------------------------- MODULE MCPCallRecovery ----------------------------
EXTENDS Naturals

(* A mounted execution tool sees one of three verified outcomes. "expired"
   is a protocol rejection before execution, "unknown" may have an effect.
   One replacement session is allowed for the call. Other failures reserve
   the task's admission gate until the human starts an explicit retry.
   A manager request already admitted receives cancellation on block. *)
CONSTANTS
    RetryUncertain,
    IgnoreBlock,
    RenewAgain,
    LeaveManagerRunning

VARIABLES phase, attempts, first, result, nextWork, cancelManager
vars == <<phase, attempts, first, result, nextWork, cancelManager>>

Outcomes == {"ok", "expired", "unknown"}
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
NeverRecovered == ~(phase = "done" /\ attempts = 2)
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
