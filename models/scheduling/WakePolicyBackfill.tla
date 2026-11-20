--------------------------- MODULE WakePolicyBackfill ---------------------------
(* One turn has a fixed neutral backlog. An explicit prohibition may arrive
   between successful verification and the atomic schedule write. Successful
   classifications persist in source order. Each retry observes that prefix.
   With a live caller and a responsive classifier, fair tool progress must
   settle the request. Cancellation, classifier errors and wall time are
   covered by implementation tests, outside this finite model. *)
EXTENDS Naturals

CONSTANTS BatchSize, InitialInputs, PerTurnBudget, SkipWritePolicyRecheck
ASSUME BatchSize > 0 /\ InitialInputs > BatchSize

VARIABLES inputCount, classified, atStart, used, phase, result, pendingCount, writes
vars == <<inputCount, classified, atStart, used, phase, result, pendingCount, writes>>

HasDenial == inputCount > InitialInputs
Pending == inputCount - classified

Init ==
    /\ inputCount = InitialInputs
    /\ classified = 0 /\ atStart = 0 /\ used = 0
    /\ phase = "idle" /\ result = "none" /\ pendingCount = 0 /\ writes = 0

StartCall ==
    /\ phase = "idle"
    /\ phase' = "classifying"
    /\ atStart' = classified
    /\ used' = IF PerTurnBudget THEN used ELSE 0
    /\ UNCHANGED <<inputCount, classified, result, pendingCount, writes>>

Classify ==
    /\ phase = "classifying" /\ Pending > 0 /\ used < BatchSize
    /\ classified' = classified + 1 /\ used' = used + 1
    /\ UNCHANGED <<inputCount, atStart, phase, result, pendingCount, writes>>

FinishCheck ==
    /\ phase = "classifying" /\ (Pending = 0 \/ used = BatchSize)
    /\ IF Pending > 0
       THEN /\ phase' = "idle" /\ result' = "batch_pending" /\ pendingCount' = Pending
       ELSE IF HasDenial
            THEN /\ phase' = "settled" /\ result' = "denied" /\ pendingCount' = 0
            ELSE /\ phase' = "write" /\ UNCHANGED <<result, pendingCount>>
    /\ UNCHANGED <<inputCount, classified, atStart, used, writes>>

NewHumanDenial ==
    /\ phase = "write" /\ ~HasDenial
    /\ inputCount' = inputCount + 1
    /\ UNCHANGED <<classified, atStart, used, phase, result, pendingCount, writes>>

Commit ==
    /\ phase = "write"
    /\ IF Pending > 0 /\ ~SkipWritePolicyRecheck
       THEN /\ phase' = "idle" /\ result' = "write_pending" /\ pendingCount' = Pending
            /\ UNCHANGED writes
       ELSE /\ phase' = "settled" /\ result' = "armed" /\ pendingCount' = 0
            /\ writes' = writes + 1
    /\ UNCHANGED <<inputCount, classified, atStart, used>>

ToolProgress == StartCall \/ Classify \/ FinishCheck \/ Commit
Next == ToolProgress \/ NewHumanDenial
Spec == Init /\ [][Next]_vars /\ WF_vars(ToolProgress)

TypeOK ==
    /\ inputCount \in InitialInputs..(InitialInputs + 1)
    /\ classified \in 0..inputCount /\ atStart \in 0..classified
    /\ used \in 0..BatchSize /\ writes \in 0..1
    /\ phase \in {"idle", "classifying", "write", "settled"}
    /\ result \in {"none", "batch_pending", "write_pending", "denied", "armed"}
    /\ pendingCount \in 0..inputCount

EveryArmAuthorized ==
    /\ (writes > 0 => Pending = 0 /\ ~HasDenial /\ result = "armed")
    /\ (result = "armed" => writes = 1)

PendingBackfillAdvances ==
    (phase = "idle" /\ result \in {"batch_pending", "write_pending"}) =>
    /\ pendingCount = Pending /\ pendingCount > 0
    /\ (result = "batch_pending" => classified > atStart)

EventuallySettled == <>(phase = "settled")

NeverBatchPending ==
    ~(phase = "idle" /\ result = "batch_pending" /\ pendingCount = InitialInputs - BatchSize)
NeverSameTurnArm ==
    ~(result = "armed" /\ atStart = BatchSize /\ writes = 1)
NeverWritePending ==
    ~(phase = "idle" /\ result = "write_pending" /\ pendingCount = 1 /\ writes = 0)
NeverDenialSettles ==
    ~(result = "denied" /\ Pending = 0 /\ HasDenial /\ writes = 0)
=================================================================================
