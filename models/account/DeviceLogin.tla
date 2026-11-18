----------------------------- MODULE DeviceLogin -----------------------------
EXTENDS TLC

(* One login attempt, an explicit host-launch request, a missing/failed/working
   launcher and a safe/unsafe Gateway response. Capability alone is not launch
   intent. Host.runLogin verifies the URL, publishes safe status, then polls.
   Terminal status replaces the pending value. Remote approval is not assumed. *)
CONSTANTS
    RequireBrowser,
    PublishUnsafe,
    RetainTerminalURL,
    BypassApproval,
    LaunchWithoutOptIn

VARIABLES phase, browser, openRequested, launched, safe, url, approved, credential
vars == <<phase, browser, openRequested, launched, safe, url, approved, credential>>

Init ==
    /\ phase = "idle"
    /\ browser \in {"missing", "failed", "working"}
    /\ openRequested \in BOOLEAN /\ launched = FALSE
    /\ safe \in BOOLEAN
    /\ url = "none" /\ approved = FALSE /\ credential = FALSE

Start ==
    /\ phase = "idle"
    /\ phase' = IF RequireBrowser /\ browser # "working" THEN "blocked" ELSE "starting"
    /\ UNCHANGED <<browser, openRequested, launched, safe, url, approved, credential>>

Response ==
    /\ phase = "starting"
    /\ phase' = IF safe \/ PublishUnsafe THEN "pending" ELSE "rejected"
    /\ url' = IF safe THEN "safe" ELSE IF PublishUnsafe THEN "unsafe" ELSE "none"
    /\ UNCHANGED <<browser, openRequested, launched, safe, approved, credential>>

OpenHostBrowser ==
    /\ phase = "pending" /\ url = "safe" /\ browser # "missing" /\ ~launched
    /\ (openRequested \/ LaunchWithoutOptIn)
    /\ launched' = TRUE
    /\ UNCHANGED <<phase, browser, openRequested, safe, url, approved, credential>>

HumanApproves ==
    /\ phase = "pending" /\ ~approved
    /\ approved' = TRUE
    /\ UNCHANGED <<phase, browser, openRequested, launched, safe, url, credential>>

Activate ==
    /\ phase = "pending" /\ (approved \/ BypassApproval)
    /\ phase' = "authorized" /\ credential' = TRUE
    /\ url' = IF RetainTerminalURL THEN url ELSE "none"
    /\ UNCHANGED <<browser, openRequested, launched, safe, approved>>

CancelOrExpire ==
    /\ phase \in {"starting", "pending"}
    /\ phase' = "terminal"
    /\ url' = IF RetainTerminalURL THEN url ELSE "none"
    /\ UNCHANGED <<browser, openRequested, launched, safe, approved, credential>>

Next == Start \/ Response \/ OpenHostBrowser \/ HumanApproves \/ Activate \/ CancelOrExpire
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ phase \in {"idle", "blocked", "starting", "pending", "rejected", "authorized", "terminal"}
    /\ browser \in {"missing", "failed", "working"} /\ safe \in BOOLEAN
    /\ openRequested \in BOOLEAN /\ launched \in BOOLEAN
    /\ url \in {"none", "safe", "unsafe"}
    /\ approved \in BOOLEAN /\ credential \in BOOLEAN
ManualLoginAvailable == phase # "blocked"
OnlyValidatedURL == url # "unsafe"
TerminalHidesURL == phase \in {"authorized", "terminal", "rejected"} => url = "none"
ApprovalRequired == credential => approved /\ safe
BrowserLaunchRequiresOptIn == launched => openRequested

NeverManualAuthorized == ~(phase = "authorized" /\ browser # "working")
NeverDefaultAuthorizedWithLauncher == ~(phase = "authorized" /\ browser = "working" /\ ~openRequested /\ ~launched)
NeverOptedInLaunch == ~(openRequested /\ browser = "working" /\ launched)
NeverOptedInFallback == ~(phase = "authorized" /\ openRequested /\ browser = "missing" /\ ~launched)
NeverUnsafeRejected == ~(phase = "rejected" /\ ~safe)
NeverCancelledPending == ~(phase = "terminal" /\ safe)
=============================================================================
