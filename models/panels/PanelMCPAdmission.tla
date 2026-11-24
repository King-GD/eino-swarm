------------------------- MODULE PanelMCPAdmission -------------------------
EXTENDS Naturals

CONSTANTS
    BindLatestSettings,
    RestoreOtherDestination,
    SkipSourcePolicy,
    IgnoreWorkerScope,
    SkipSessionAdmission,
    SkipRemoteRecheck

ASSUME \A flag \in {BindLatestSettings, RestoreOtherDestination,
                    SkipSourcePolicy, IgnoreWorkerScope,
                    SkipSessionAdmission, SkipRemoteRecheck} : flag \in BOOLEAN

(* One role, one server ID, two destinations and one source call. A missing
   captured role abstracts an absent, disabled, ambiguous or unmounted tool.
   The role and connection admitted with conversationTools remain frozen when
   resolveMCPSurface runs later. A restored binding also needs the original
   destination digest; credentials and real catalogs are outside this model.

   runPanelSource samples current account/planning policy once. runEASMSource
   then filters the actual worker tools: an exact direct tool or this server's
   call proxy authorizes the role; its list proxy does not. CallTool checks the
   live remote switch before acquire and again after connect/catalog lookup.
   Manager.acquire and Reload use their own lock, separate from config publish.

   IgnoreWorkerScope restores the missing worker filter fixed by b71b592b.
   TestEASMWorkerSourceUsesItsRegisteredMCPScope exercises its real model,
   Registry and HTTP boundary, including a worker with only the list proxy.

   No action combines acquire, remote recheck or dispatch. Reload retires idle
   sessions; it does not cancel a call that already acquired its entry. A live
   disable after the last check may precede dispatch. Recorded admission facts
   describe that call, never its later configuration. These finite safety and
   reachability checks assume successful connection/schema validation; they do
   not prove remote effects, cancellation, Go locking or eventual completion. *)

Roles == {"none", "original", "later"}
Destinations == {"none", "original", "replacement"}
Scopes == {"exact-direct", "same-server-call", "same-server-list",
           "other-direct", "other-server-call", "none"}
Phases == {"captured", "mounted", "source", "entry-passed", "acquired",
           "checked", "dispatched", "completed", "refused"}

VARIABLES capturedRole, caller, workerScope,
          currentRole, currentDestination, serverEnabled, remoteEnabled,
          policyAllowed, managerDestination, managerEnabled,
          phase, receiptRole, receiptDestination, activeRole, activeDestination,
          restored, restoreDestination, sourceRole, sourceDestination,
          sourcePolicyAllowed, sourceScopeAllowed, acquiredAllowed,
          recheckAllowed, refusal, sent, sentAfterRemoteDisable,
          sentAfterPolicyChange, sentAfterRetirement

vars == <<capturedRole, caller, workerScope, currentRole, currentDestination,
          serverEnabled, remoteEnabled, policyAllowed, managerDestination,
          managerEnabled, phase, receiptRole, receiptDestination, activeRole,
          activeDestination, restored, restoreDestination, sourceRole,
          sourceDestination, sourcePolicyAllowed, sourceScopeAllowed,
          acquiredAllowed, recheckAllowed, refusal, sent,
          sentAfterRemoteDisable, sentAfterPolicyChange, sentAfterRetirement>>
initialVars == <<capturedRole, caller, workerScope>>
configVars == <<currentRole, currentDestination, serverEnabled, remoteEnabled,
                policyAllowed, managerDestination, managerEnabled>>
bindingVars == <<receiptRole, receiptDestination, activeRole, activeDestination,
                 restored, restoreDestination>>
sourceVars == <<sourceRole, sourceDestination, sourcePolicyAllowed,
                sourceScopeAllowed>>
callVars == <<acquiredAllowed, recheckAllowed, refusal, sent,
              sentAfterRemoteDisable, sentAfterPolicyChange, sentAfterRetirement>>

Init ==
    /\ capturedRole \in {"none", "original"}
    /\ caller \in {"manager", "worker"} /\ workerScope \in Scopes
    /\ currentRole = capturedRole /\ currentDestination = "original"
    /\ serverEnabled = TRUE /\ remoteEnabled = TRUE /\ policyAllowed = TRUE
    /\ managerDestination = "original" /\ managerEnabled = TRUE
    /\ phase = "captured"
    /\ receiptRole = "none" /\ receiptDestination = "none"
    /\ activeRole = "none" /\ activeDestination = "none"
    /\ restored = FALSE /\ restoreDestination = "none"
    /\ sourceRole = "none" /\ sourceDestination = "none"
    /\ sourcePolicyAllowed = FALSE /\ sourceScopeAllowed = FALSE
    /\ acquiredAllowed = FALSE /\ recheckAllowed = FALSE
    /\ refusal = "none" /\ sent = FALSE
    /\ sentAfterRemoteDisable = FALSE /\ sentAfterPolicyChange = FALSE
    /\ sentAfterRetirement = FALSE

(* Each edit may happen between the actual admission steps. In particular,
   config publication precedes the separately locked Manager.Reload. *)
ChangeRole ==
    /\ currentRole = capturedRole
    /\ currentRole' = IF capturedRole = "none" THEN "original" ELSE "later"
    /\ UNCHANGED <<initialVars, currentDestination, serverEnabled, remoteEnabled,
                   policyAllowed, managerDestination, managerEnabled, phase,
                   bindingVars, sourceVars, callVars>>

ReplaceDestination ==
    /\ currentDestination = "original"
    /\ currentDestination' = "replacement"
    /\ UNCHANGED <<initialVars, currentRole, serverEnabled, remoteEnabled,
                   policyAllowed, managerDestination, managerEnabled, phase,
                   bindingVars, sourceVars, callVars>>

DisableServer ==
    /\ serverEnabled /\ serverEnabled' = FALSE
    /\ UNCHANGED <<initialVars, currentRole, currentDestination, remoteEnabled,
                   policyAllowed, managerDestination, managerEnabled, phase,
                   bindingVars, sourceVars, callVars>>

DisableRemote ==
    /\ remoteEnabled /\ remoteEnabled' = FALSE
    /\ UNCHANGED <<initialVars, currentRole, currentDestination, serverEnabled,
                   policyAllowed, managerDestination, managerEnabled, phase,
                   bindingVars, sourceVars, callVars>>

RestrictPolicy ==
    /\ policyAllowed /\ policyAllowed' = FALSE
    /\ UNCHANGED <<initialVars, currentRole, currentDestination, serverEnabled,
                   remoteEnabled, managerDestination, managerEnabled, phase,
                   bindingVars, sourceVars, callVars>>

ReloadManager ==
    /\ (managerDestination # currentDestination \/ managerEnabled # serverEnabled)
    /\ managerDestination' = currentDestination /\ managerEnabled' = serverEnabled
    /\ UNCHANGED <<initialVars, currentRole, currentDestination, serverEnabled,
                   remoteEnabled, policyAllowed, phase, bindingVars,
                   sourceVars, callVars>>

Mount ==
    /\ phase = "captured"
    /\ LET role == IF BindLatestSettings THEN currentRole ELSE capturedRole
           dest == IF role = "none" THEN "none"
                   ELSE IF BindLatestSettings THEN currentDestination ELSE "original"
       IN /\ receiptRole' = role /\ receiptDestination' = dest
          /\ activeRole' = role /\ activeDestination' = dest
    /\ phase' = "mounted"
    /\ UNCHANGED <<initialVars, configVars, restored, restoreDestination,
                   sourceVars, callVars>>

(* No source is running during this bounded reconstruction. Existing names
   are assumed mounted; the implementation separately checks their presence. *)
Restore ==
    /\ phase = "mounted" /\ ~restored
    /\ restored' = TRUE /\ restoreDestination' = currentDestination
    /\ LET available == receiptRole # "none" /\ serverEnabled /\
                        (currentDestination = receiptDestination \/ RestoreOtherDestination)
       IN /\ activeRole' = IF available THEN receiptRole ELSE "none"
          /\ activeDestination' = IF available THEN currentDestination ELSE "none"
    /\ UNCHANGED <<initialVars, configVars, phase, receiptRole,
                   receiptDestination, sourceVars, callVars>>

BeginSource ==
    /\ phase = "mounted"
    /\ LET scopeAllowed == caller = "manager" \/
                           workerScope \in {"exact-direct", "same-server-call"}
           available == (policyAllowed \/ SkipSourcePolicy) /\
                        (scopeAllowed \/ IgnoreWorkerScope)
       IN /\ sourcePolicyAllowed' = policyAllowed
          /\ sourceScopeAllowed' = scopeAllowed
          /\ sourceRole' = IF available THEN activeRole ELSE "none"
          /\ sourceDestination' = IF available THEN activeDestination ELSE "none"
    /\ phase' = "source"
    /\ UNCHANGED <<initialVars, configVars, bindingVars, callVars>>

CheckEntry ==
    /\ phase = "source"
    /\ phase' = IF sourceRole # "none" /\ remoteEnabled THEN "entry-passed" ELSE "refused"
    /\ refusal' = IF sourceRole = "none" THEN "binding"
                  ELSE IF ~remoteEnabled THEN "remote-entry" ELSE "none"
    /\ UNCHANGED <<initialVars, configVars, bindingVars, sourceVars,
                   acquiredAllowed, recheckAllowed, sent, sentAfterRemoteDisable,
                   sentAfterPolicyChange, sentAfterRetirement>>

Acquire ==
    /\ phase = "entry-passed"
    /\ LET available == managerEnabled /\ managerDestination = sourceDestination
       IN /\ acquiredAllowed' = available
          /\ phase' = IF available \/ SkipSessionAdmission THEN "acquired" ELSE "refused"
          /\ refusal' = IF available \/ SkipSessionAdmission THEN "none" ELSE "session"
    /\ UNCHANGED <<initialVars, configVars, bindingVars, sourceVars,
                   recheckAllowed, sent, sentAfterRemoteDisable,
                   sentAfterPolicyChange, sentAfterRetirement>>

(* Successful connection and required-argument validation are abstracted.
   This is the second live switch read, not an atomic dispatch operation. *)
RecheckRemote ==
    /\ phase = "acquired"
    /\ recheckAllowed' = remoteEnabled
    /\ phase' = IF remoteEnabled \/ SkipRemoteRecheck THEN "checked" ELSE "refused"
    /\ refusal' = IF remoteEnabled \/ SkipRemoteRecheck THEN "none" ELSE "remote-recheck"
    /\ UNCHANGED <<initialVars, configVars, bindingVars, sourceVars,
                   acquiredAllowed, sent, sentAfterRemoteDisable,
                   sentAfterPolicyChange, sentAfterRetirement>>

Dispatch ==
    /\ phase = "checked"
    /\ phase' = "dispatched" /\ sent' = TRUE
    /\ sentAfterRemoteDisable' = ~remoteEnabled
    /\ sentAfterPolicyChange' = ~policyAllowed
    /\ sentAfterRetirement' = (~managerEnabled \/ managerDestination # sourceDestination)
    /\ UNCHANGED <<initialVars, configVars, bindingVars, sourceVars,
                   acquiredAllowed, recheckAllowed, refusal>>

Complete ==
    /\ phase = "dispatched" /\ phase' = "completed"
    /\ UNCHANGED <<initialVars, configVars, bindingVars, sourceVars, callVars>>

Next == ChangeRole \/ ReplaceDestination \/ DisableServer \/ DisableRemote \/
        RestrictPolicy \/ ReloadManager \/ Mount \/ Restore \/ BeginSource \/
        CheckEntry \/ Acquire \/ RecheckRemote \/ Dispatch \/ Complete
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ capturedRole \in {"none", "original"} /\ caller \in {"manager", "worker"}
    /\ workerScope \in Scopes /\ currentRole \in Roles
    /\ currentDestination \in {"original", "replacement"}
    /\ managerDestination \in {"original", "replacement"} /\ phase \in Phases
    /\ receiptRole \in Roles /\ activeRole \in Roles /\ sourceRole \in Roles
    /\ receiptDestination \in Destinations /\ activeDestination \in Destinations
    /\ sourceDestination \in Destinations /\ restoreDestination \in Destinations
    /\ refusal \in {"none", "binding", "remote-entry", "session", "remote-recheck"}
    /\ \A flag \in {serverEnabled, remoteEnabled, policyAllowed, managerEnabled,
                    restored, sourcePolicyAllowed, sourceScopeAllowed,
                    acquiredAllowed, recheckAllowed, sent, sentAfterRemoteDisable,
                    sentAfterPolicyChange, sentAfterRetirement} : flag \in BOOLEAN

TurnBindingFrozen == phase # "captured" =>
    (receiptRole = capturedRole /\
     receiptDestination = IF capturedRole = "none" THEN "none" ELSE "original")
RestoreDestinationKept == restored /\ activeRole # "none" =>
    activeDestination = receiptDestination
SourcePolicyRequired == sourceRole # "none" => sourcePolicyAllowed
WorkerScopeRequired == sourceRole # "none" => sourceScopeAllowed
SessionAdmissionRequired == sent => acquiredAllowed
RemoteRecheckRequired == sent => recheckAllowed

(* Negated witnesses prove useful success/refusal paths and deliberately
   retain calls admitted before a later disable. None asserts liveness. *)
NeverDirectWorkerCall == ~(sent /\ caller = "worker" /\ workerScope = "exact-direct")
NeverProxyWorkerCall == ~(sent /\ caller = "worker" /\ workerScope = "same-server-call")
NeverListOnlyRefusal == ~(phase = "refused" /\ refusal = "binding" /\
    activeRole # "none" /\ sourcePolicyAllowed /\ caller = "worker" /\
    workerScope = "same-server-list")
NeverLateRoleIgnored == ~(phase = "mounted" /\ capturedRole = "none" /\
    currentRole = "original" /\ receiptRole = "none")
NeverRestrictedSource == ~(phase = "refused" /\ refusal = "binding" /\
    activeRole # "none" /\ ~sourcePolicyAllowed)
NeverRestoreDestinationRefused == ~(restored /\ receiptRole # "none" /\
    restoreDestination = "replacement" /\ activeRole = "none")
NeverRemoteRecheckRefused == ~(phase = "refused" /\ refusal = "remote-recheck")
NeverSessionAdmissionRefused == ~(phase = "refused" /\ refusal = "session")
NeverLateRemoteDisableCompletes == ~(phase = "completed" /\ sentAfterRemoteDisable)
NeverAdmittedPolicyChangeCompletes == ~(phase = "completed" /\ sentAfterPolicyChange)
NeverRetiredSessionCompletes == ~(phase = "completed" /\ sentAfterRetirement)
NeverRestoredCall == ~(sent /\ restored)
=============================================================================
