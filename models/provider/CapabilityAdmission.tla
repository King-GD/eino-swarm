-------------------------- MODULE CapabilityAdmission --------------------------
EXTENDS Naturals, FiniteSets

(* A retrospective model of the 2026-11-17 account-model panic. CheckCapability
   represents guardCapability + trackedModel.policyUse. Waiting/WaitNotice is
   recordingModel.admitBackoff -> backoffGate.acquire's observer. SendHTTP and
   Complete/Cancel represent generateOnce/streamOnce and the terminal recorder.
   NestedCapabilityWrapper restores the f6e9a9a5 composition: the outer recorder
   captures no gate and starts before the inner gate admits a request.
   GuardNilObserverOnly is the insufficient patch that suppresses that observer
   but retains the premature outer call. DropSourcePolicy and SkipCapabilityCheck
   remove independent guards; SkipFinalAdmission sends on a stale admission.
   The gate's clock/probe health is abstracted to blocked/ready, not proved here.
   Permissions are checked before waiting and again before binding the request.
   SendHTTP represents that final binding as one step. *)
CONSTANTS
    NestedCapabilityWrapper,
    GuardNilObserverOnly,
    SkipCapabilityCheck,
    DropSourcePolicy,
    SkipFinalAdmission,
    MaxRequests

Requests == 1..MaxRequests
Phases == {"new", "waiting", "http", "stream", "done", "cancelled", "denied", "source_denied", "panicked"}
Active == {"waiting", "http", "stream"}

VARIABLES phase, accepted, sent, recorded, waitCalled, noticeSeen,
          observerGate, grant, grantChanged, blocked, faultSeen,
          sourceFrozen, sourceChanged, sourceViolation, switchedAfterCompletion
vars == <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
          observerGate, grant, grantChanged, blocked, faultSeen,
          sourceFrozen, sourceChanged, sourceViolation, switchedAfterCompletion>>

Init ==
    /\ phase = [r \in Requests |-> "new"]
    /\ accepted = [r \in Requests |-> FALSE]
    /\ sent = [r \in Requests |-> FALSE]
    /\ recorded = [r \in Requests |-> FALSE]
    /\ waitCalled = [r \in Requests |-> FALSE]
    /\ noticeSeen = [r \in Requests |-> FALSE]
    /\ observerGate = [r \in Requests |-> 0]
    /\ grant \in BOOLEAN
    /\ grantChanged = FALSE
    /\ blocked = FALSE
    /\ faultSeen = FALSE
    /\ sourceFrozen = FALSE
    /\ sourceChanged = FALSE
    /\ sourceViolation = FALSE
    /\ switchedAfterCompletion = FALSE

CheckCapability(r) ==
    /\ phase[r] = "new"
    /\ accepted' = [accepted EXCEPT ![r] = grant]
    /\ phase' = [phase EXCEPT ![r] =
         IF ~grant /\ ~SkipCapabilityCheck THEN "denied"
         ELSE IF sourceFrozen /\ ~DropSourcePolicy THEN "source_denied"
         ELSE "waiting"]
    /\ UNCHANGED <<sent, recorded, waitCalled, noticeSeen, observerGate,
                   grant, grantChanged, blocked, faultSeen, sourceFrozen,
                   sourceChanged, sourceViolation, switchedAfterCompletion>>

WaitNotice(r) ==
    /\ phase[r] = "waiting" /\ blocked /\ ~noticeSeen[r]
    /\ noticeSeen' = [noticeSeen EXCEPT ![r] = TRUE]
    /\ IF NestedCapabilityWrapper /\ GuardNilObserverOnly
       THEN UNCHANGED <<phase, waitCalled, observerGate>>
       ELSE /\ waitCalled' = [waitCalled EXCEPT ![r] = TRUE]
            /\ observerGate' = [observerGate EXCEPT ![r] = IF NestedCapabilityWrapper THEN 0 ELSE 1]
            /\ phase' = [phase EXCEPT ![r] = IF NestedCapabilityWrapper THEN "panicked" ELSE @]
    /\ UNCHANGED <<accepted, sent, recorded, grant, grantChanged,
                   blocked, faultSeen, sourceFrozen, sourceChanged,
                   sourceViolation, switchedAfterCompletion>>

SendHTTP(r) ==
    /\ phase[r] = "waiting" /\ ~blocked
    /\ IF sourceFrozen /\ ~DropSourcePolicy /\ ~SkipFinalAdmission
       THEN /\ phase' = [phase EXCEPT ![r] = "source_denied"]
            /\ UNCHANGED <<sent, accepted, sourceViolation>>
       ELSE IF ~grant /\ ~SkipCapabilityCheck /\ ~SkipFinalAdmission
            THEN /\ phase' = [phase EXCEPT ![r] = "denied"]
                 /\ UNCHANGED <<sent, accepted, sourceViolation>>
            ELSE /\ phase' = [phase EXCEPT ![r] = "http"]
                 /\ sent' = [sent EXCEPT ![r] = TRUE]
                 /\ accepted' = [accepted EXCEPT ![r] = grant]
                 /\ sourceViolation' = (sourceViolation \/ sourceFrozen)
    /\ UNCHANGED <<recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, blocked, faultSeen,
                   sourceFrozen, sourceChanged, switchedAfterCompletion>>

OpenStream(r) ==
    /\ phase[r] = "http"
    /\ phase' = [phase EXCEPT ![r] = "stream"]
    /\ UNCHANGED <<accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, blocked, faultSeen,
                   sourceFrozen, sourceChanged, sourceViolation, switchedAfterCompletion>>

Complete(r) ==
    /\ phase[r] \in {"http", "stream"}
    /\ phase' = [phase EXCEPT ![r] = "done"]
    /\ recorded' = [recorded EXCEPT ![r] = TRUE]
    /\ UNCHANGED <<accepted, sent, waitCalled, noticeSeen, observerGate,
                   grant, grantChanged, blocked, faultSeen, sourceFrozen,
                   sourceChanged, sourceViolation, switchedAfterCompletion>>

Cancel(r) ==
    /\ phase[r] \in Active \cup {"new"}
    /\ recorded' = [recorded EXCEPT ![r] = sent[r] \/ (phase[r] = "waiting" /\ NestedCapabilityWrapper)]
    /\ phase' = [phase EXCEPT ![r] = "cancelled"]
    /\ UNCHANGED <<accepted, sent, waitCalled, noticeSeen, observerGate,
                   grant, grantChanged, blocked, faultSeen, sourceFrozen,
                   sourceChanged, sourceViolation, switchedAfterCompletion>>

FailOrigin ==
    /\ ~faultSeen
    /\ faultSeen' = TRUE /\ blocked' = TRUE
    /\ UNCHANGED <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, sourceFrozen, sourceChanged,
                   sourceViolation, switchedAfterCompletion>>

GateReady ==
    /\ blocked /\ blocked' = FALSE
    /\ UNCHANGED <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, faultSeen, sourceFrozen,
                   sourceChanged, sourceViolation, switchedAfterCompletion>>

ChangeGrant ==
    /\ ~grantChanged /\ grantChanged' = TRUE /\ grant' = ~grant
    /\ UNCHANGED <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, blocked, faultSeen, sourceFrozen, sourceChanged,
                   sourceViolation, switchedAfterCompletion>>

AcquireSourceChange ==
    /\ ~sourceFrozen /\ ~sourceChanged
    /\ sourceFrozen' = TRUE
    /\ switchedAfterCompletion' = (\E r \in Requests : phase[r] = "done")
    /\ UNCHANGED <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, blocked, faultSeen, sourceChanged, sourceViolation>>

ReleaseSourceChange ==
    /\ sourceFrozen /\ sourceFrozen' = FALSE /\ sourceChanged' = TRUE
    /\ UNCHANGED <<phase, accepted, sent, recorded, waitCalled, noticeSeen,
                   observerGate, grant, grantChanged, blocked, faultSeen,
                   sourceViolation, switchedAfterCompletion>>

Next == (\E r \in Requests : CheckCapability(r) \/ WaitNotice(r) \/ SendHTTP(r)
                             \/ OpenStream(r) \/ Complete(r) \/ Cancel(r))
        \/ FailOrigin \/ GateReady \/ ChangeGrant \/ AcquireSourceChange \/ ReleaseSourceChange
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ phase \in [Requests -> Phases]
    /\ accepted \in [Requests -> BOOLEAN]
    /\ sent \in [Requests -> BOOLEAN] /\ recorded \in [Requests -> BOOLEAN]
    /\ waitCalled \in [Requests -> BOOLEAN] /\ noticeSeen \in [Requests -> BOOLEAN]
    /\ observerGate \in [Requests -> {0, 1}]
    /\ <<grant, grantChanged, blocked, faultSeen, sourceFrozen, sourceChanged,
          sourceViolation, switchedAfterCompletion>> \in [1..8 -> BOOLEAN]
NoPanic == \A r \in Requests : phase[r] # "panicked"
ObserverOwnsGate == \A r \in Requests : waitCalled[r] => observerGate[r] = 1
CallsRequireHTTP == \A r \in Requests : recorded[r] => sent[r]
CapabilityRequired == \A r \in Requests : sent[r] => accepted[r]
SourcePolicyRespected == ~sourceViolation

NeverCooldownWait == \A r \in Requests : ~waitCalled[r]
NeverCancelledWait == ~\E r \in Requests : phase[r] = "cancelled" /\ waitCalled[r] /\ ~sent[r]
NeverSuccessfulCall == ~\E r \in Requests : phase[r] = "done"
NeverSwitchDuringStream == ~(sourceFrozen /\ (\E r \in Requests : phase[r] = "stream"))
NeverPermissionRefusal == ~\E r \in Requests : phase[r] = "denied"
NeverSourceRefusal == ~\E r \in Requests : phase[r] = "source_denied"
NeverConcurrentRequests == Cardinality({r \in Requests : phase[r] \in {"http", "stream"}}) < 2
NeverSwitchAfterCompletion == ~switchedAfterCompletion
=============================================================================
