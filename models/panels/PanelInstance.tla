----------------------------- MODULE PanelInstance ---------------------------
CONSTANT ShowInitial, IgnoreClose, StopTurnOnClose

VARIABLES opened, observed, source, closed, task
vars == <<opened, observed, source, closed, task>>

Init == /\ opened = FALSE /\ observed = FALSE /\ source = "idle" /\ closed = FALSE /\ task = "running"
Open == /\ ~opened /\ opened' = TRUE /\ UNCHANGED <<observed, source, closed, task>>
Start == /\ opened /\ ~closed /\ source = "idle"
         /\ source' = "running" /\ UNCHANGED <<opened, observed, closed, task>>
Publish == /\ opened /\ ~observed /\ (~closed \/ IgnoreClose)
           /\ observed' = TRUE /\ UNCHANGED <<opened, source, closed, task>>
Finish == /\ source = "running" /\ source' = "finished"
          /\ UNCHANGED <<opened, observed, closed, task>>
Recover == /\ source = "running" /\ source' = "interrupted"
           /\ UNCHANGED <<opened, observed, closed, task>>
Visible == opened /\ (~closed \/ IgnoreClose) /\ (observed \/ source = "running" \/ ShowInitial)
Close == /\ Visible /\ ~closed /\ closed' = TRUE
         /\ source' = IF source = "running" THEN "interrupted" ELSE source
         /\ task' = IF StopTurnOnClose THEN "stopped" ELSE task
         /\ UNCHANGED <<opened, observed>>
Next == Open \/ Start \/ Publish \/ Finish \/ Recover \/ Close
Spec == Init /\ [][Next]_vars

TypeOK == /\ opened \in BOOLEAN /\ observed \in BOOLEAN /\ closed \in BOOLEAN
          /\ source \in {"idle", "running", "finished", "interrupted"}
          /\ task \in {"running", "stopped"}
VisibilityRequiresEvidence == Visible => (observed \/ source = "running")
ClosedStaysHidden == closed => ~Visible /\ source # "running"
TaskUnaffected == task = "running"
NeverRunningEmpty == ~(Visible /\ source = "running" /\ ~observed)
NeverRetainedZero == ~(Visible /\ observed /\ source = "idle")
NeverClosedRunning == ~(closed /\ source = "interrupted")
NeverSettledEmpty == ~(source = "finished" /\ ~observed /\ ~Visible)
=============================================================================
