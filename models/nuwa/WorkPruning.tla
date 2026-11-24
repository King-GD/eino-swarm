---------------------------- MODULE WorkPruning ----------------------------
(* A bounded work graph after a committed snapshot. A fact and artifact form
   a two-edge prerequisite path to a task; an independent fact can collapse
   and return under its original ID. This checks the projection and version
   protocol. Browser and Store tests cover their production implementations. *)
EXTENDS Naturals, FiniteSets

CONSTANTS PruneDeletesRecord, DropTransitivePrerequisite, KeepEveryInactive,
          RememberPruned, ReconfirmOnRestore, ReconfirmOnPatch,
          IgnorePinnedVersion, IgnoreInactiveSource, MaxVersion

Nodes == {"source", "bridge", "result", "independent"}
Dependents == {"bridge", "result"}
Source(id) == IF id = "bridge" THEN "source" ELSE "bridge"
Prerequisites(id) == IF id \in Dependents THEN {Source(id)} ELSE {}
Ancestors(id) ==
    CASE id = "bridge" -> {"source"}
      [] id = "result" -> {"source", "bridge"}
      [] OTHER -> {}

InitialStatus(id) == IF id = "result" THEN "done" ELSE "reported"
InvalidStatus(id) ==
    CASE id = "result" -> "cancelled"
      [] id = "bridge" -> "superseded"
      [] OTHER -> "retracted"
InvalidStatuses == {"cancelled", "superseded", "retracted"}
Stages == {"original", "inactive", "restored"}

VARIABLES rows, stage, version, pin, confirmed, patched, hidden, showAll
vars == <<rows, stage, version, pin, confirmed, patched, hidden, showAll>>

Status(id, stages) ==
    IF stages[id] = "inactive" THEN InvalidStatus(id) ELSE InitialStatus(id)
Active(stages, stored) == {id \in stored : Status(id, stages) \notin InvalidStatuses}
Needed(stages, stored) ==
    LET active == Active(stages, stored) IN
        (active \cup UNION {Ancestors(id) : id \in active}) \cap stored

(* An independent dependency-closure specification above checks the bounded
   expansion below. A one-level traversal loses the grandparent whenever
   both prerequisites of the active result are inactive. *)
Expand(ids) == ids \cup UNION {Prerequisites(id) : id \in ids}
DefaultShown ==
    IF KeepEveryInactive THEN rows
    ELSE LET direct == Expand(Active(stage, rows))
             keep == IF DropTransitivePrerequisite THEN direct ELSE Expand(direct)
         IN (keep \cap rows) \ hidden
Graph == IF showAll THEN rows ELSE DefaultShown
Warnings ==
    {id \in rows \cap Dependents :
        /\ Source(id) \in rows
        /\ \/ ~IgnorePinnedVersion /\ pin[id] # version[Source(id)]
           \/ ~IgnoreInactiveSource /\ Status(Source(id), stage) \in InvalidStatuses}

Init ==
    /\ rows = Nodes /\ stage = [id \in Nodes |-> "original"]
    /\ version = [id \in Nodes |-> 1]
    /\ pin = [id \in Dependents |-> 1] /\ confirmed = pin
    /\ patched = FALSE /\ hidden = {} /\ showAll = FALSE

Invalidate(id) ==
    /\ id \in rows /\ stage[id] = "original" /\ version[id] < MaxVersion
    /\ stage' = [stage EXCEPT ![id] = "inactive"]
    /\ version' = [version EXCEPT ![id] = @ + 1]
    /\ rows' = IF PruneDeletesRecord /\ id \notin Needed(stage', rows)
               THEN rows \ {id} ELSE rows
    /\ hidden' = IF RememberPruned THEN hidden \cup (rows' \ Needed(stage', rows')) ELSE hidden
    /\ UNCHANGED <<pin, confirmed, patched, showAll>>

Restore(id) ==
    /\ id \in rows /\ stage[id] = "inactive" /\ version[id] < MaxVersion
    /\ stage' = [stage EXCEPT ![id] = "restored"]
    /\ version' = [version EXCEPT ![id] = @ + 1]
    /\ pin' = IF ReconfirmOnRestore
              THEN [d \in Dependents |-> IF Source(d) = id THEN version'[id] ELSE pin[d]]
              ELSE pin
    /\ UNCHANGED <<rows, confirmed, patched, hidden, showAll>>

(* A correction can change a dependent in other ways without acknowledging
   a new prerequisite version. Only the explicit dependency replacement does. *)
PatchResult ==
    /\ ~patched /\ "result" \in rows /\ version["result"] < MaxVersion
    /\ patched' = TRUE /\ version' = [version EXCEPT !["result"] = @ + 1]
    /\ pin' = IF ReconfirmOnPatch THEN [pin EXCEPT !["result"] = version["bridge"]] ELSE pin
    /\ UNCHANGED <<rows, stage, confirmed, hidden, showAll>>

Reconfirm(id) ==
    /\ id \in rows /\ Source(id) \in rows /\ version[id] < MaxVersion
    /\ pin[id] # version[Source(id)]
    /\ pin' = [pin EXCEPT ![id] = version[Source(id)]]
    /\ confirmed' = [confirmed EXCEPT ![id] = version[Source(id)]]
    /\ version' = [version EXCEPT ![id] = @ + 1]
    /\ UNCHANGED <<rows, stage, patched, hidden, showAll>>

ToggleView ==
    /\ showAll' = ~showAll
    /\ UNCHANGED <<rows, stage, version, pin, confirmed, patched, hidden>>

Next ==
    \/ \E id \in Nodes : Invalidate(id) \/ Restore(id)
    \/ PatchResult
    \/ \E id \in Dependents : Reconfirm(id)
    \/ ToggleView
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ rows \subseteq Nodes /\ stage \in [Nodes -> Stages]
    /\ version \in [Nodes -> 1..MaxVersion]
    /\ pin \in [Dependents -> 1..MaxVersion] /\ confirmed \in [Dependents -> 1..MaxVersion]
    /\ hidden \subseteq Nodes /\ patched \in BOOLEAN /\ showAll \in BOOLEAN

StoredIdentitySurvivesPruning == rows = Nodes
ActiveRecordsAppear == Active(stage, rows) \subseteq DefaultShown
InactiveAncestorsStayVisible == Needed(stage, rows) \ Active(stage, rows) \subseteq DefaultShown
UnneededInactiveRecordsCollapse == DefaultShown \subseteq Needed(stage, rows)
PinsRequireExplicitReconfirmation == pin = confirmed
WarningsReflectStatusAndVersion ==
    \A id \in rows \cap Dependents : Source(id) \in rows =>
        (id \notin Warnings) = (pin[id] = version[Source(id)] /\ Source(id) \in Active(stage, rows))

NeverCollapseWholeBranch ==
    ~({ "source", "bridge", "result" } \cap DefaultShown = {} /\ "independent" \in DefaultShown)
NeverRetainInactiveChain ==
    ~(stage["source"] = "inactive" /\ stage["bridge"] = "inactive"
      /\ "result" \in Active(stage, rows) /\ {"source", "bridge", "result"} \subseteq DefaultShown)
NeverRestoreOriginalID ==
    ~(stage["independent"] = "restored" /\ "independent" \in DefaultShown)
NeverRestoreWithStaleDependent ==
    ~(stage["source"] = "restored" /\ "source" \in DefaultShown
      /\ "bridge" \in Active(stage, rows) /\ "bridge" \in Warnings
      /\ confirmed["bridge"] # version["source"])
NeverClearConfirmedDependency ==
    ~(stage["source"] = "restored" /\ confirmed["bridge"] = version["source"] /\ "bridge" \notin Warnings)
NeverRevealCollapsedRecord ==
    ~(showAll /\ "independent" \in Graph /\ "independent" \notin DefaultShown)
=============================================================================
