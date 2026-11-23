---------------------------- MODULE MCPCallRecovery ----------------------------
EXTENDS Naturals

(* "expired" is a protocol rejection before execution. A Streamable HTTP
   call's local "deadline" leaves the effect unknown and the session usable
   by independent work. "missing" is produced by the local catalog check,
   before a wire call. "wrong_type" has the same local proof. "rate_limited"
   is a tool result received after dispatch: it permits other work but has
   no proof of when execution stopped. Unclassified tool errors reserve the task's
   admission gate. A manager already admitted receives cancellation on block. *)
CONSTANTS
    RetryUncertain,
    IgnoreBlock,
    RenewAgain,
    LeaveManagerRunning,
    BlockDeadline,
    BlockMissing,
    DispatchMissing,
    AllowToolError,
    BlockRateLimit,
    BlockWrongType,
    DispatchWrongType

VARIABLES phase, attempts, first, result, nextWork, cancelManager
vars == <<phase, attempts, first, result, nextWork, cancelManager>>

WireOutcomes == {"ok", "expired", "deadline", "unknown", "tool_error", "rate_limited"}
Outcomes == WireOutcomes \cup {"missing", "wrong_type"}
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
NeverNextWorkAfterDeadline == ~(result = "deadline" /\ nextWork)
MissingKeepsAdmission == result /= "missing" \/ phase # "blocked"
MissingNeverDispatches == result /= "missing" \/ attempts = (IF first = "expired" THEN 1 ELSE 0)
RateLimitKeepsAdmission == result /= "rate_limited" \/ phase # "blocked"
WrongTypeKeepsAdmission == result /= "wrong_type" \/ phase # "blocked"
WrongTypeNeverDispatches == result /= "wrong_type" \/ attempts = (IF first = "expired" THEN 1 ELSE 0)
NeverAdmitsWorkAfterRateLimit == ~(result = "rate_limited" /\ nextWork)
NeverAdmitsWorkAfterWrongType == ~(result = "wrong_type" /\ nextWork)
RemoteErrorBlocks == result /= "tool_error" \/ phase \in {"resolve", "blocked"}
NeverAdmitsWorkAfterMissing == ~(result = "missing" /\ nextWork)
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

RejectArgument(outcome) ==
    /\ phase = "send"
    /\ result' = outcome
    /\ phase' = "resolve"
    /\ UNCHANGED <<attempts, first, nextWork, cancelManager>>

Decide ==
    /\ phase = "resolve"
    /\ phase' = IF result = "ok" THEN "done"
       ELSE IF (result = "expired" \/ RetryUncertain) /\ (attempts = 1 \/ RenewAgain)
            THEN "send"
            ELSE IF result = "deadline" /\ ~BlockDeadline THEN "done"
            ELSE IF result = "missing" /\ ~BlockMissing THEN "done"
            ELSE IF result = "wrong_type" /\ ~BlockWrongType THEN "done"
            ELSE IF result = "rate_limited" /\ ~BlockRateLimit THEN "done"
            ELSE IF result = "tool_error" /\ AllowToolError THEN "done"
                 ELSE "blocked"
    /\ cancelManager' = (phase' = "blocked" /\ ~LeaveManagerRunning)
    /\ UNCHANGED <<attempts, first, result, nextWork>>

OtherWork ==
    /\ phase = "done" \/ (phase = "blocked" /\ IgnoreBlock)
    /\ nextWork' = TRUE
    /\ UNCHANGED <<phase, attempts, first, result, cancelManager>>

Next == (\E outcome \in WireOutcomes: Send(outcome))
        \/ (IF DispatchMissing THEN Send("missing") ELSE RejectArgument("missing"))
        \/ (IF DispatchWrongType THEN Send("wrong_type") ELSE RejectArgument("wrong_type"))
        \/ Decide \/ OtherWork
Spec == Init /\ [][Next]_vars
=============================================================================
