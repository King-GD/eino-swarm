--------------------------- MODULE PanelProjection ---------------------------
EXTENDS Naturals, Sequences, FiniteSets

(* Two panels share a cursor but have independent revisions. An atomic GET
   captures real committed state, then its response may cross later SSE
   publications/deletion or a conversation visit change. One GET and four
   publications bound the state space. There is no network fairness or quiet
   stream assumption: an eligible P is merged while Q keeps advancing. *)
CONSTANTS
    RejectStaleCursor,
    ForgetDelete,
    OverwriteNewer,
    IgnoreVisit,
    KeepCoveredObservation

Panels == {"p", "q"}
Zero == [p \in Panels |-> 0]
Initial == [p \in Panels |-> IF p = "q" THEN 1 ELSE 0]
Event(p, r, c, gone) == [panel |-> p, revision |-> r, cursor |-> c, deleted |-> gone]

VARIABLES server, serverCursor, journal, delivered, visit, view, observed,
          observedAt, tombstone, cursor, snapshotCursor, request, reply,
          lostNew, resurrected, rewound, staleApplied, crossedQ

vars == <<server, serverCursor, journal, delivered, visit, view, observed,
          observedAt, tombstone, cursor, snapshotCursor, request, reply,
          lostNew, resurrected, rewound, staleApplied, crossedQ>>

Init ==
    /\ server = Initial /\ serverCursor = 1 /\ journal = <<>> /\ delivered = 0
    /\ visit = 1 /\ view = Initial /\ observed = Initial /\ observedAt = Initial
    /\ tombstone = Zero /\ cursor = 1 /\ snapshotCursor = 1
    /\ request = "idle" /\ reply = [visit |-> 1, cursor |-> 1, panels |-> Initial]
    /\ lostNew = FALSE /\ resurrected = FALSE /\ rewound = FALSE
    /\ staleApplied = FALSE /\ crossedQ = FALSE

Publish(p) ==
    /\ Len(journal) < 4 /\ server[p] < 2 /\ tombstone[p] = 0
    /\ (p # "p" \/ ~\E i \in 1..Len(journal) : journal[i].deleted)
    /\ (p # "p" \/ server[p] = 0 \/ server[p] = 1)
    /\ server' = [server EXCEPT ![p] = @ + 1]
    /\ serverCursor' = serverCursor + 1
    /\ journal' = Append(journal, Event(p, server[p] + 1, serverCursor + 1, FALSE))
    /\ UNCHANGED <<delivered, visit, view, observed, observedAt, tombstone,
          cursor, snapshotCursor, request, reply, lostNew, resurrected,
          rewound, staleApplied, crossedQ>>

Delete ==
    /\ Len(journal) < 4 /\ server["p"] > 0
    /\ ~\E i \in 1..Len(journal) : journal[i].deleted
    /\ server' = [server EXCEPT !["p"] = 0]
    /\ serverCursor' = serverCursor + 1
    /\ journal' = Append(journal, Event("p", 3, serverCursor + 1, TRUE))
    /\ UNCHANGED <<delivered, visit, view, observed, observedAt, tombstone,
          cursor, snapshotCursor, request, reply, lostNew, resurrected,
          rewound, staleApplied, crossedQ>>

(* New P has no installed definition in the projection until GET returns.
   Q's ordinary patch applies immediately against its actual local base. *)
Deliver ==
    /\ delivered < Len(journal)
    /\ LET e == journal[delivered + 1]
           own == visit = 1 /\ e.cursor > cursor
       IN /\ delivered' = delivered + 1
          /\ cursor' = IF own THEN e.cursor ELSE cursor
          /\ observed' = IF own THEN [observed EXCEPT ![e.panel] = e.revision] ELSE observed
          /\ observedAt' = IF own THEN [observedAt EXCEPT ![e.panel] = e.cursor] ELSE observedAt
          /\ tombstone' = IF own /\ e.deleted THEN [tombstone EXCEPT ![e.panel] = e.cursor] ELSE tombstone
          /\ view' = IF ~own THEN view
                       ELSE IF e.deleted THEN [view EXCEPT ![e.panel] = 0]
                       ELSE IF view[e.panel] > 0 /\ view[e.panel] + 1 = e.revision
                            THEN [view EXCEPT ![e.panel] = e.revision] ELSE view
          /\ crossedQ' = (crossedQ \/ (own /\ e.panel = "q" /\ request = "inflight" /\ e.cursor > reply.cursor))
    /\ UNCHANGED <<server, serverCursor, journal, visit, snapshotCursor,
          request, reply, lostNew, resurrected, rewound, staleApplied>>

Capture ==
    /\ visit = 1 /\ request = "idle"
    /\ request' = "inflight"
    /\ reply' = [visit |-> visit, cursor |-> serverCursor, panels |-> server]
    /\ UNCHANGED <<server, serverCursor, journal, delivered, visit, view,
          observed, observedAt, tombstone, cursor, snapshotCursor,
          lostNew, resurrected, rewound, staleApplied, crossedQ>>

Merged(p) ==
    IF ~ForgetDelete /\ tombstone[p] > reply.cursor THEN 0
    ELSE IF ~OverwriteNewer /\ view[p] > reply.panels[p] /\
            (reply.panels[p] > 0 \/ observedAt[p] > reply.cursor) THEN view[p]
    ELSE reply.panels[p]

Respond ==
    /\ request = "inflight" /\ request' = "returned"
    /\ LET owner == reply.visit = visit \/ IgnoreVisit
           eligible == owner /\ reply.cursor >= snapshotCursor /\
                       (~RejectStaleCursor \/ reply.cursor >= cursor)
           next == IF eligible THEN [p \in Panels |-> Merged(p)] ELSE view
           expectedP == reply.visit = visit /\ reply.panels["p"] > 0 /\
                        reply.panels["p"] >= observed["p"] /\ tombstone["p"] <= reply.cursor
       IN /\ view' = next
          /\ observed' = IF eligible /\ ~KeepCoveredObservation
                         THEN [p \in Panels |-> IF reply.panels[p] = 0 /\ observedAt[p] <= reply.cursor
                                               THEN 0 ELSE observed[p]] ELSE observed
          /\ cursor' = IF eligible /\ reply.cursor > cursor THEN reply.cursor ELSE cursor
          /\ snapshotCursor' = IF eligible THEN reply.cursor ELSE snapshotCursor
          /\ lostNew' = (lostNew \/ (expectedP /\ next["p"] = 0))
          /\ resurrected' = (resurrected \/ (eligible /\ tombstone["p"] > reply.cursor /\ next["p"] > 0))
          /\ rewound' = (rewound \/ (eligible /\ \E p \in Panels : next[p] < view[p] /\ observedAt[p] > reply.cursor /\ tombstone[p] = 0))
          /\ staleApplied' = (staleApplied \/ (eligible /\ reply.visit # visit))
    /\ UNCHANGED <<server, serverCursor, journal, delivered, visit,
          observedAt, tombstone, reply, crossedQ>>

SelectOther ==
    /\ visit = 1 /\ visit' = 2 /\ view' = Zero /\ observed' = Zero
    /\ observedAt' = Zero /\ tombstone' = Zero /\ cursor' = 0 /\ snapshotCursor' = 0
    /\ UNCHANGED <<server, serverCursor, journal, delivered, request, reply,
          lostNew, resurrected, rewound, staleApplied, crossedQ>>

Next == (\E p \in Panels : Publish(p)) \/ Delete \/ Deliver \/ Capture \/ Respond \/ SelectOther
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ server \in [Panels -> 0..2] /\ view \in [Panels -> 0..2]
    /\ observed \in [Panels -> 0..3] /\ observedAt \in [Panels -> 0..5]
    /\ tombstone \in [Panels -> 0..5] /\ serverCursor \in 1..5
    /\ cursor \in 0..5 /\ snapshotCursor \in 0..5 /\ visit \in 1..2
    /\ delivered \in 0..Len(journal) /\ Len(journal) <= 4
    /\ request \in {"idle", "inflight", "returned"}
    /\ lostNew \in BOOLEAN /\ resurrected \in BOOLEAN /\ rewound \in BOOLEAN
    /\ staleApplied \in BOOLEAN /\ crossedQ \in BOOLEAN

EligibleSnapshotAdmitted == ~lostNew
DeletedNeverResurrects == ~resurrected
NewerRevisionRetained == ~rewound
PreviousVisitIgnored == ~staleApplied
(* Reconnection may miss a deletion frame. A later atomic snapshot that no
   longer contains that panel settles its covered observation; otherwise the
   client forever requests a missing version that cannot exist. *)
CoveredAbsenceSettles ==
    \A p \in Panels : request = "returned" /\ reply.visit = visit /\
                      reply.panels[p] = 0 /\ observedAt[p] <= reply.cursor
                      => observed[p] = 0 \/ tombstone[p] > 0

NeverCrossedKnownUpdate == ~crossedQ
NeverNewPanelMerged == ~(request = "returned" /\ crossedQ /\ view["p"] > 0)
NeverDeleteDuringGet == ~(request = "inflight" /\ tombstone["p"] > reply.cursor)
NeverVisitDuringGet == ~(request = "inflight" /\ visit # reply.visit)
NeverReconnectAbsence ==
    ~(request = "returned" /\ reply.panels["p"] = 0 /\ view["p"] = 0 /\
      delivered < Len(journal) /\
      (\E i \in 1..delivered : journal[i].panel = "p" /\ ~journal[i].deleted) /\
      (\E i \in (delivered + 1)..Len(journal) : journal[i].deleted))
=============================================================================
