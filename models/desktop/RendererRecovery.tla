----------------------------- MODULE RendererRecovery -----------------------------
EXTENDS Naturals

(* Paint acknowledgement for one desktop window. A WebKit snapshot that
   matches the current challenge is not enough when the visible body is one
   flat colour. A missed, hidden, refused or timed-out capture is not flat:
   the WebKit result stands. The first foreground failure reloads once; a
   later failure opens a successor on the same engine, at most MaxReplacements
   times. Leaving the foreground starts a new grace and does not spend that
   budget.

   IgnorePresentedFrame is the decision shipped through v0.1.77.
   UnknownCountsAsFlat treats a missing capture as a blank body.
   BlankSkipsReload opens a successor without the one reload.
   BackgroundSpendsRecovery spends the budget while the window is away,
   including during grace.

   Pixel thresholds, capture timeouts and the wall-clock grace are not this
   protocol. The successor is the same engine because this model has no
   second engine. *)

CONSTANTS
    IgnorePresentedFrame,
    UnknownCountsAsFlat,
    BlankSkipsReload,
    BackgroundSpendsRecovery,
    MaxProbes,
    MaxReplacements

VARIABLES
    foreground, presented, webkit, appliedPresented, appliedWebkit,
    decision, phase, grace, reloads, replacements, reloadsAway, probes, sampled

vars == <<foreground, presented, webkit, appliedPresented, appliedWebkit,
          decision, phase, grace, reloads, replacements, reloadsAway, probes, sampled>>

Bodies == {"none", "content", "flat", "unknown"}
Paints == {"none", "match", "miss"}
Decisions == {"none", "healthy", "blank", "mismatch"}
Phases == {"watching", "reloading", "exhausted"}

Init ==
    /\ foreground = TRUE
    /\ presented = "none"
    /\ webkit = "none"
    /\ appliedPresented = "none"
    /\ appliedWebkit = "none"
    /\ decision = "none"
    /\ phase = "watching"
    /\ grace = 1
    /\ reloads = 0
    /\ replacements = 0
    /\ reloadsAway = 0
    /\ probes = 0
    /\ sampled = FALSE

TypeOK ==
    /\ foreground \in BOOLEAN
    /\ presented \in Bodies
    /\ webkit \in Paints
    /\ appliedPresented \in Bodies
    /\ appliedWebkit \in Paints
    /\ decision \in Decisions
    /\ phase \in Phases
    /\ grace \in 0..1
    /\ reloads \in 0..1
    /\ replacements \in 0..MaxReplacements
    /\ reloadsAway \in 0..1
    /\ probes \in 0..MaxProbes
    /\ sampled \in BOOLEAN
    /\ (sampled => presented /= "none" /\ webkit /= "none")
    /\ (~sampled => presented = "none" /\ webkit = "none")

Classify(body, paint) ==
    IF paint = "miss" THEN "mismatch"
    ELSE IF body = "flat" /\ ~IgnorePresentedFrame THEN "blank"
    ELSE IF body = "unknown" /\ UnknownCountsAsFlat THEN "blank"
    ELSE "healthy"

Sample ==
    /\ foreground
    /\ ~sampled
    /\ probes < MaxProbes
    /\ phase \in {"watching", "reloading"}
    /\ \E body \in {"content", "flat", "unknown"}:
         \E paint \in {"match", "miss"}:
            /\ presented' = body
            /\ webkit' = paint
            /\ sampled' = TRUE
            /\ probes' = probes + 1
            /\ UNCHANGED <<foreground, appliedPresented, appliedWebkit, decision,
                           phase, grace, reloads, replacements, reloadsAway>>

Acknowledge ==
    /\ sampled
    /\ IF ~foreground
       THEN /\ sampled' = FALSE
            /\ presented' = "none"
            /\ webkit' = "none"
            /\ UNCHANGED <<foreground, appliedPresented, appliedWebkit, decision,
                           phase, grace, reloads, replacements, reloadsAway, probes>>
       ELSE /\ LET kind == Classify(presented, webkit)
               IN /\ decision' = kind
                  /\ appliedPresented' = presented
                  /\ appliedWebkit' = webkit
                  /\ sampled' = FALSE
                  /\ presented' = "none"
                  /\ webkit' = "none"
                  /\ IF kind = "healthy"
                     THEN /\ grace' = 1
                          /\ phase' = "watching"
                     ELSE UNCHANGED <<grace, phase>>
                  /\ UNCHANGED <<foreground, reloads, replacements, reloadsAway, probes>>

\* A successor returns to watching so the next attempt needs a new failure.
\* The last attempt stops.
Successor ==
    /\ replacements' = replacements + 1
    /\ IF replacements + 1 >= MaxReplacements
       THEN /\ phase' = "exhausted"
            /\ UNCHANGED <<decision, appliedPresented, appliedWebkit, grace>>
       ELSE /\ phase' = "watching"
            /\ decision' = "none"
            /\ appliedPresented' = "none"
            /\ appliedWebkit' = "none"
            /\ grace' = 1

Spend ==
    /\ phase = "watching"
    /\ decision \in {"blank", "mismatch"}
    /\ (foreground \/ BackgroundSpendsRecovery)
    /\ (grace = 0 \/ (BackgroundSpendsRecovery /\ ~foreground))

Tick ==
    /\ ~sampled
    /\ \/ /\ foreground
          /\ phase = "watching"
          /\ grace > 0
          /\ decision \in {"blank", "mismatch"}
          /\ grace' = grace - 1
          /\ UNCHANGED <<foreground, presented, webkit, appliedPresented, appliedWebkit,
                         decision, phase, reloads, replacements, reloadsAway, probes, sampled>>
       \/ /\ Spend
          /\ IF reloads = 0 /\ ~BlankSkipsReload
             THEN /\ reloads' = 1
                  /\ phase' = "reloading"
                  /\ reloadsAway' = IF foreground THEN reloadsAway ELSE reloadsAway + 1
                  /\ UNCHANGED <<foreground, presented, webkit, appliedPresented, appliedWebkit,
                                 decision, grace, replacements, probes, sampled>>
             ELSE /\ Successor
                  /\ reloadsAway' = IF foreground THEN reloadsAway ELSE reloadsAway + 1
                  /\ UNCHANGED <<foreground, presented, webkit, reloads, probes, sampled>>

FinishReload ==
    /\ phase = "reloading"
    /\ ~sampled
    /\ foreground
    /\ IF decision = "healthy"
       THEN /\ phase' = "watching"
            /\ UNCHANGED <<decision, appliedPresented, appliedWebkit, grace, replacements>>
       ELSE Successor
    /\ UNCHANGED <<foreground, presented, webkit, reloads, reloadsAway, probes, sampled>>

Leave ==
    /\ foreground
    /\ foreground' = FALSE
    /\ grace' = 1
    /\ sampled' = FALSE
    /\ presented' = "none"
    /\ webkit' = "none"
    /\ UNCHANGED <<appliedPresented, appliedWebkit, decision, phase, reloads,
                   replacements, reloadsAway, probes>>

Enter ==
    /\ ~foreground
    /\ foreground' = TRUE
    /\ grace' = 1
    /\ UNCHANGED <<presented, webkit, appliedPresented, appliedWebkit, decision,
                   phase, reloads, replacements, reloadsAway, probes, sampled>>

Idle ==
    /\ \/ phase = "exhausted"
       \/ (probes = MaxProbes /\ ~sampled /\ phase /= "reloading")
    /\ UNCHANGED vars

Next ==
    \/ Sample
    \/ Acknowledge
    \/ Tick
    \/ FinishReload
    \/ Leave
    \/ Enter
    \/ Idle

Spec == Init /\ [][Next]_vars

\* A matching challenge is not healthy paint when the visible body is flat.
VisibleFlatIsNotHealthy ==
    decision = "healthy" => appliedPresented /= "flat"

\* Losing the presented frame does not cancel a WebKit match.
UnknownMatchStaysHealthy ==
    (appliedPresented = "unknown" /\ appliedWebkit = "match") => decision = "healthy"

\* The one reload happens before any successor.
ReplacementFollowsReload ==
    replacements > 0 => reloads >= 1

\* Background, including a fresh grace, does not spend a reload or a successor.
RecoveryStaysInForeground ==
    reloadsAway = 0

NeverHealthyContent == decision /= "healthy" \/ appliedPresented /= "content"

NeverBlank == decision /= "blank"

NeverReloaded == reloads = 0

NeverReplaced == replacements = 0

NeverUnknownHealthy == ~(decision = "healthy" /\ appliedPresented = "unknown")

=============================================================================
