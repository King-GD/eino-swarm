---------------------------- MODULE PeerInventory ----------------------------
EXTENDS TLC

(* One read-only peer observation. A foreign UID proves that a historic PID
   now names another process; its children are not queue ownership. Completed
   identity-matched claims remain required. Same-UID ambiguity stays unknown.
   Maps to current_peers/dead_peer_is_quiet, not to process termination. *)
CONSTANTS
    NumericPIDOwnsChildren,
    IgnoreClaims,
    IgnoreSameUIDChildren,
    IgnoreActiveController

VARIABLES controller, children, receipts, decision
vars == <<controller, children, receipts, decision>>

Init == /\ controller \in {"active", "absent", "sameUIDUnknown", "foreignUID"}
        /\ children \in BOOLEAN
        /\ receipts \in {"complete", "missing", "liveEffect"}
        /\ decision = "unobserved"

Observe ==
    /\ decision = "unobserved"
    /\ decision' = IF controller = "active" /\ ~IgnoreActiveController THEN "busy"
       ELSE IF (receipts = "complete" \/ IgnoreClaims)
           /\ ~(children /\ (controller # "foreignUID" \/ NumericPIDOwnsChildren)
                        /\ ~IgnoreSameUIDChildren)
       THEN "quiet" ELSE "unknown"
    /\ UNCHANGED <<controller, children, receipts>>

Spec == Init /\ [][Observe]_vars
TypeOK == /\ controller \in {"active", "absent", "sameUIDUnknown", "foreignUID"}
          /\ children \in BOOLEAN /\ receipts \in {"complete", "missing", "liveEffect"}
          /\ decision \in {"unobserved", "busy", "quiet", "unknown"}
ClaimsRequired == decision = "quiet" => receipts = "complete"
SameUIDChildrenRefuse == controller = "sameUIDUnknown" /\ children => decision # "quiet"
ActiveControllerBusy == controller = "active" => decision # "quiet"
ForeignReuseDoesNotBlock ==
    controller = "foreignUID" /\ receipts = "complete" /\ decision # "unobserved"
    => decision = "quiet"
NeverForeignQuiet == ~(controller = "foreignUID" /\ children /\ decision = "quiet")
NeverUnresolvedRefused == ~(receipts # "complete" /\ decision = "unknown")
=============================================================================
