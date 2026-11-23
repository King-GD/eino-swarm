--------------------------- MODULE VisibleHistory ---------------------------
EXTENDS Naturals, FiniteSets

(* One idle tail projects two stored work rows without delivering raw events.
   The older visible message, live append, expansion, and rewind can interleave.
   EventStream checks durable ordering; this model checks the initial cursors
   and projection ownership passed to that stream. *)
CONSTANTS UseVisibleCursor, SkipOlderMessage, KeepRewoundWork
VARIABLES stored, rendered, deferred, before, since, opened, paged, expanded, rewound
vars == <<stored, rendered, deferred, before, since, opened, paged, expanded, rewound>>

Init == /\ stored = 1..4
        /\ rendered = {} /\ deferred = {}
        /\ before = 0 /\ since = 0
        /\ opened = FALSE /\ paged = FALSE /\ expanded = FALSE /\ rewound = FALSE

Open == /\ ~opened
        /\ opened' = TRUE
        /\ rendered' = {2} /\ deferred' = {3, 4}
        /\ before' = IF SkipOlderMessage THEN 1 ELSE 2
        /\ since' = IF UseVisibleCursor THEN 2 ELSE 4
        /\ UNCHANGED <<stored, paged, expanded, rewound>>

Append == /\ opened /\ ~rewound /\ 5 \notin stored
          /\ stored' = stored \cup {5}
          /\ UNCHANGED <<rendered, deferred, before, since, opened, paged, expanded, rewound>>

CatchUp == /\ opened /\ ~rewound /\ (\E row \in stored : row > since)
           /\ rendered' = rendered \cup {row \in stored : row > since}
           /\ since' = IF 5 \in stored THEN 5 ELSE 4
           /\ UNCHANGED <<stored, deferred, before, opened, paged, expanded, rewound>>

Older == /\ opened /\ ~paged
         /\ rendered' = rendered \cup {row \in stored : row < before}
         /\ before' = 1 /\ paged' = TRUE
         /\ UNCHANGED <<stored, deferred, since, opened, expanded, rewound>>

Expand == /\ opened /\ deferred # {} /\ ~expanded
          /\ expanded' = TRUE
          /\ UNCHANGED <<stored, rendered, deferred, before, since, opened, paged, rewound>>

Rewind == /\ opened /\ ~rewound
          /\ rewound' = TRUE /\ since' = 6
          /\ stored' = {row \in stored : row < 3}
          /\ rendered' = {row \in rendered : row < 3}
          /\ deferred' = IF KeepRewoundWork THEN deferred ELSE {}
          /\ UNCHANGED <<before, opened, paged, expanded>>

Next == Open \/ Append \/ CatchUp \/ Older \/ Expand \/ Rewind
Spec == Init /\ [][Next]_vars

TypeOK == /\ stored \subseteq 1..5 /\ rendered \subseteq 1..5 /\ deferred \subseteq 1..5
          /\ before \in 0..2 /\ since \in 0..6
          /\ <<opened, paged, expanded, rewound>> \in [1..4 -> BOOLEAN]
NoDeferredReplay == rendered \cap deferred = {}
PreviousMessageReached == paged => 1 \in rendered
WorkStillStored == deferred \subseteq stored
NeverOpenAndCatchUp == ~(opened /\ 5 \in rendered)
NeverPageExpandRewind == ~(paged /\ expanded /\ rewound)
=============================================================================
