-------------------------- MODULE PanelFrameDelivery --------------------------
EXTENDS Naturals

(* One iframe, two data revisions and at most one outstanding callback. Apply
   and acknowledge are separate asynchronous steps. Visibility can change
   during either step. No fairness or eventual render success is assumed:
   a hung callback must expose a bounded local failure, not claim delivery. *)
CONSTANTS
    ForgetHiddenResync,
    AckBeforeApply,
    ExecuteIncompatible,
    SilentTimeout

VARIABLES visible, desired, sent, acked, applied, ready, executed, compatible,
          inflight, callbackDone, needsSnapshot, resynced, failed, explained,
          hiddenFailure, advancedInflight, timedOut

vars == <<visible, desired, sent, acked, applied, ready, executed, compatible,
          inflight, callbackDone, needsSnapshot, resynced, failed, explained,
          hiddenFailure, advancedInflight, timedOut>>

Init ==
    /\ visible = TRUE /\ desired = 1 /\ sent = 0 /\ acked = 0 /\ applied = 0
    /\ ready = FALSE /\ executed = FALSE /\ compatible \in BOOLEAN
    /\ inflight = FALSE /\ callbackDone = FALSE /\ needsSnapshot = TRUE
    /\ resynced = 0 /\ failed = FALSE /\ explained = FALSE
    /\ hiddenFailure = FALSE /\ advancedInflight = FALSE /\ timedOut = FALSE

Handshake ==
    /\ ~executed /\ ~failed /\ (compatible \/ ExecuteIncompatible)
    /\ executed' = TRUE /\ ready' = TRUE
    /\ UNCHANGED <<visible, desired, sent, acked, applied, compatible,
          inflight, callbackDone, needsSnapshot, resynced, failed, explained,
          hiddenFailure, advancedInflight, timedOut>>

SetVisible ==
    /\ visible' = ~visible
    /\ UNCHANGED <<desired, sent, acked, applied, ready, executed, compatible,
          inflight, callbackDone, needsSnapshot, resynced, failed, explained,
          hiddenFailure, advancedInflight, timedOut>>

Advance ==
    /\ desired = 1 /\ desired' = 2
    /\ advancedInflight' = (advancedInflight \/ inflight)
    /\ UNCHANGED <<visible, sent, acked, applied, ready, executed, compatible,
          inflight, callbackDone, needsSnapshot, resynced, failed, explained,
          hiddenFailure, timedOut>>

Send ==
    /\ ready /\ visible /\ ~failed /\ ~inflight
    /\ (needsSnapshot \/ desired > (IF ForgetHiddenResync THEN sent ELSE acked))
    /\ sent' = desired /\ inflight' = TRUE /\ callbackDone' = FALSE
    /\ needsSnapshot' = FALSE
    /\ UNCHANGED <<visible, desired, acked, applied, ready, executed, compatible,
          resynced, failed, explained, hiddenFailure, advancedInflight, timedOut>>

Apply ==
    /\ inflight /\ ~callbackDone /\ ~failed
    /\ applied' = sent /\ callbackDone' = TRUE
    /\ UNCHANGED <<visible, desired, sent, acked, ready, executed, compatible,
          inflight, needsSnapshot, resynced, failed, explained,
          hiddenFailure, advancedInflight, timedOut>>

Ack ==
    /\ inflight /\ ~failed /\ (callbackDone \/ AckBeforeApply)
    /\ acked' = sent /\ inflight' = FALSE /\ explained' = FALSE
    /\ UNCHANGED <<visible, desired, sent, applied, ready, executed, compatible,
          callbackDone, needsSnapshot, resynced, failed,
          hiddenFailure, advancedInflight, timedOut>>

(* The SDK sends error then resync after a rejected callback. A second failure
   at this revision stops automatically; an explicit Retry is outside this
   bounded channel lifetime. Hidden recovery must survive until visible. *)
Reject ==
    /\ inflight /\ ~callbackDone /\ ~failed
    /\ inflight' = FALSE /\ explained' = TRUE
    /\ failed' = (resynced = desired) /\ resynced' = desired
    /\ needsSnapshot' = ~(ForgetHiddenResync /\ ~visible)
    /\ hiddenFailure' = (hiddenFailure \/ ~visible)
    /\ UNCHANGED <<visible, desired, sent, acked, applied, ready, executed,
          compatible, callbackDone, advancedInflight, timedOut>>

Timeout ==
    /\ compatible /\ ~failed /\ (~ready \/ inflight)
    /\ failed' = TRUE /\ explained' = ~SilentTimeout /\ timedOut' = TRUE
    /\ inflight' = FALSE
    /\ UNCHANGED <<visible, desired, sent, acked, applied, ready, executed,
          compatible, callbackDone, needsSnapshot, resynced,
          hiddenFailure, advancedInflight>>

Next == Handshake \/ SetVisible \/ Advance \/ Send \/ Apply \/ Ack \/ Reject \/ Timeout
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ desired \in 1..2 /\ sent \in 0..2 /\ acked \in 0..2 /\ applied \in 0..2
    /\ visible \in BOOLEAN /\ ready \in BOOLEAN /\ executed \in BOOLEAN
    /\ compatible \in BOOLEAN /\ inflight \in BOOLEAN /\ callbackDone \in BOOLEAN
    /\ needsSnapshot \in BOOLEAN /\ resynced \in 0..2 /\ failed \in BOOLEAN
    /\ explained \in BOOLEAN /\ hiddenFailure \in BOOLEAN
    /\ advancedInflight \in BOOLEAN /\ timedOut \in BOOLEAN

AcknowledgedWasApplied == acked <= applied
RecoveryRemainsSendable ==
    ready /\ visible /\ ~failed /\ ~inflight /\ applied < desired => ENABLED Send
OnlyCompatibleExecutes == executed => compatible
FailureIsVisible == failed => explained

NeverHiddenFailure == ~hiddenFailure
NeverHiddenRecovery == ~(hiddenFailure /\ visible /\ acked = desired /\ ~failed)
NeverAdvanceInflight == ~advancedInflight
NeverTimeout == ~timedOut
=============================================================================
