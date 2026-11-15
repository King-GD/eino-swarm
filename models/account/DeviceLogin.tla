----------------------------- MODULE DeviceLogin -----------------------------
EXTENDS TLC

(* One login attempt, a missing/failed/working host browser and a safe/unsafe
   Gateway response. The browser is a convenience; human approval stays required.
   Host.runLogin verifies the URL, publishes safe status, then polls. Terminal
   status replaces the pending value. No assumption promises remote approval. *)
CONSTANTS
    RequireBrowser,
    PublishUnsafe,
    RetainTerminalURL,
    BypassApproval

VARIABLES phase, browser, safe, url, approved, credential
vars == <<phase, browser, safe, url, approved, credential>>

Init ==
    /\ phase = "idle"
    /\ browser \in {"missing", "failed", "working"}
    /\ safe \in BOOLEAN
    /\ url = "none" /\ approved = FALSE /\ credential = FALSE

Start ==
    /\ phase = "idle"
    /\ phase' = IF RequireBrowser /\ browser # "working" THEN "blocked" ELSE "starting"
    /\ UNCHANGED <<browser, safe, url, approved, credential>>

Response ==
    /\ phase = "starting"
    /\ phase' = IF safe \/ PublishUnsafe THEN "pending" ELSE "rejected"
    /\ url' = IF safe THEN "safe" ELSE IF PublishUnsafe THEN "unsafe" ELSE "none"
    /\ UNCHANGED <<browser, safe, approved, credential>>

HumanApproves ==
    /\ phase = "pending" /\ ~approved
    /\ approved' = TRUE
    /\ UNCHANGED <<phase, browser, safe, url, credential>>

Activate ==
    /\ phase = "pending" /\ (approved \/ BypassApproval)
    /\ phase' = "authorized" /\ credential' = TRUE
    /\ url' = IF RetainTerminalURL THEN url ELSE "none"
    /\ UNCHANGED <<browser, safe, approved>>

CancelOrExpire ==
    /\ phase \in {"starting", "pending"}
    /\ phase' = "terminal"
    /\ url' = IF RetainTerminalURL THEN url ELSE "none"
    /\ UNCHANGED <<browser, safe, approved, credential>>

Next == Start \/ Response \/ HumanApproves \/ Activate \/ CancelOrExpire
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ phase \in {"idle", "blocked", "starting", "pending", "rejected", "authorized", "terminal"}
    /\ browser \in {"missing", "failed", "working"} /\ safe \in BOOLEAN
    /\ url \in {"none", "safe", "unsafe"}
    /\ approved \in BOOLEAN /\ credential \in BOOLEAN
ManualLoginAvailable == phase # "blocked"
OnlyValidatedURL == url # "unsafe"
TerminalHidesURL == phase \in {"authorized", "terminal", "rejected"} => url = "none"
ApprovalRequired == credential => approved /\ safe

NeverManualAuthorized == ~(phase = "authorized" /\ browser # "working")
NeverUnsafeRejected == ~(phase = "rejected" /\ ~safe)
NeverCancelledPending == ~(phase = "terminal" /\ safe)
=============================================================================
