--------------------------- MODULE PanelProducer ---------------------------
(***************************************************************************)
(* Two distinct collected assets and one duplicate receipt. Collector input *)
(* identity, actual HTTP observation, worker submission and reducer apply   *)
(* are separate stages. Stop cannot erase an already observed response.     *)
(*                                                                         *)
(* DropObservedResult re-finds the real startWorkers cancellation select:   *)
(* probe has returned, but Stop wins before its terminal event is queued.   *)
(* result_receipt_order_test.go gates the actual loopback probe at that      *)
(* boundary and demonstrates the lost 403 receipt before the fix.           *)
(* FinishBeforeJoin seeds an early return after collection but before every *)
(* worker and its earlier terminal receipts have joined the reducer.        *)
(* NonCanonicalIdentity restores admission of equivalent default-port       *)
(* spellings as different assets, enqueuing another request for the same     *)
(* logical origin. The real CLI/HTTP regression re-finds that fault.          *)
(*                                                                         *)
(* This checks finite protocol order, not Go channel atomicity, HTTP effects *)
(* or operating-system descendant cleanup. Real boundary tests remain       *)
(* necessary for those properties.                                         *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS
    DropObservedResult,
    FinishBeforeJoin,
    NonCanonicalIdentity

ASSUME /\ DropObservedResult \in BOOLEAN
       /\ FinishBeforeJoin \in BOOLEAN
       /\ NonCanonicalIdentity \in BOOLEAN

Targets == {1, 2}
Phases == {"unseen", "queued", "running", "terminal", "settled"}
Outcomes == {"none", "responded", "interrupted"}

VARIABLES phase, admitted, duplicates, requestCount, observed, outcome,
          submitted, receipts, applied, reducerOutcome, dropped, cancelled,
          collectorJoined, workersJoined, returned

vars == <<phase, admitted, duplicates, requestCount, observed, outcome,
          submitted, receipts, applied, reducerOutcome, dropped, cancelled,
          collectorJoined, workersJoined, returned>>

Init ==
    /\ phase = [t \in Targets |-> "unseen"]
    /\ admitted = {} /\ duplicates = {}
    /\ requestCount = [t \in Targets |-> 0]
    /\ observed = {} /\ outcome = [t \in Targets |-> "none"]
    /\ submitted = {} /\ receipts = {} /\ applied = {}
    /\ reducerOutcome = [t \in Targets |-> "none"]
    /\ dropped = {} /\ cancelled = FALSE
    /\ collectorJoined = FALSE /\ workersJoined = FALSE /\ returned = FALSE

Discover(t) ==
    /\ ~returned /\ ~collectorJoined /\ ~cancelled /\ phase[t] = "unseen"
    /\ phase' = [phase EXCEPT ![t] = "queued"]
    /\ admitted' = admitted \cup {t}
    /\ UNCHANGED <<duplicates, requestCount, observed, outcome, submitted,
                   receipts, applied, reducerOutcome, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

(* Stable asset identity prevents duplicate source rows creating new jobs. *)
Duplicate(t) ==
    /\ ~returned /\ ~collectorJoined /\ ~cancelled
    /\ t \in admitted /\ t \notin duplicates
    /\ duplicates' = duplicates \cup {t}
    /\ UNCHANGED <<phase, admitted, requestCount, observed, outcome, submitted,
                   receipts, applied, reducerOutcome, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

Start(t) ==
    /\ ~returned /\ ~cancelled
    /\ (phase[t] = "queued" \/
        (NonCanonicalIdentity /\ phase[t] = "settled" /\
         t \in duplicates /\ requestCount[t] = 1))
    /\ phase' = [phase EXCEPT ![t] = "running"]
    /\ requestCount' = [requestCount EXCEPT ![t] = @ + 1]
    /\ UNCHANGED <<admitted, duplicates, observed, outcome, submitted, receipts,
                   applied, reducerOutcome, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

(* Actual client observation precedes queuing the worker's receipt. *)
ObserveHTTP(t) ==
    /\ ~returned /\ ~cancelled /\ phase[t] = "running"
    /\ phase' = [phase EXCEPT ![t] = "terminal"]
    /\ observed' = observed \cup {t}
    /\ outcome' = [outcome EXCEPT ![t] = "responded"]
    /\ UNCHANGED <<admitted, duplicates, requestCount, submitted, receipts,
                   applied, reducerOutcome, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

Stop ==
    /\ ~returned /\ ~cancelled /\ cancelled' = TRUE
    /\ UNCHANGED <<phase, admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, applied, reducerOutcome, dropped,
                   collectorJoined, workersJoined, returned>>

CancelPending(t) ==
    /\ ~returned /\ cancelled /\ phase[t] = "queued"
    /\ phase' = [phase EXCEPT ![t] = "settled"]
    /\ applied' = applied \cup {t}
    /\ reducerOutcome' = [reducerOutcome EXCEPT ![t] = "interrupted"]
    /\ UNCHANGED <<admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

CancelHTTP(t) ==
    /\ ~returned /\ cancelled /\ phase[t] = "running"
    /\ phase' = [phase EXCEPT ![t] = "terminal"]
    /\ outcome' = [outcome EXCEPT ![t] = "interrupted"]
    /\ UNCHANGED <<admitted, duplicates, requestCount, observed, submitted,
                   receipts, applied, reducerOutcome, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

Submit(t) ==
    /\ ~returned /\ phase[t] = "terminal" /\ t \notin submitted
    /\ phase' = [phase EXCEPT ![t] = "settled"]
    /\ submitted' = submitted \cup {t}
    /\ LET discard == DropObservedResult /\ cancelled /\ t \in observed
       IN /\ receipts' = IF discard THEN receipts ELSE receipts \cup {t}
          /\ dropped' = IF discard THEN dropped \cup {t} ELSE dropped
    /\ UNCHANGED <<admitted, duplicates, requestCount, observed, outcome,
                   applied, reducerOutcome, cancelled,
                   collectorJoined, workersJoined, returned>>

Apply(t) ==
    /\ ~returned /\ t \in receipts /\ t \notin applied
    /\ applied' = applied \cup {t}
    /\ reducerOutcome' = [reducerOutcome EXCEPT ![t] = outcome[t]]
    /\ UNCHANGED <<phase, admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, dropped, cancelled,
                   collectorJoined, workersJoined, returned>>

JoinCollector ==
    /\ ~returned /\ ~collectorJoined /\ collectorJoined' = TRUE
    /\ UNCHANGED <<phase, admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, applied, reducerOutcome, dropped,
                   cancelled, workersJoined, returned>>

(* Consuming workers_done joins workers and all earlier terminal events on
   the same reducer channel. Pending jobs are drained or cancelled first. *)
JoinWorkers ==
    /\ ~returned /\ ~workersJoined /\ (collectorJoined \/ cancelled)
    /\ \A t \in Targets : phase[t] \in {"unseen", "settled"}
    /\ receipts \subseteq applied
    /\ workersJoined' = TRUE
    /\ UNCHANGED <<phase, admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, applied, reducerOutcome, dropped,
                   cancelled, collectorJoined, returned>>

Return ==
    /\ ~returned /\ collectorJoined /\ (workersJoined \/ FinishBeforeJoin)
    /\ returned' = TRUE
    /\ UNCHANGED <<phase, admitted, duplicates, requestCount, observed, outcome,
                   submitted, receipts, applied, reducerOutcome, dropped,
                   cancelled, collectorJoined, workersJoined>>

Next == (\E t \in Targets : Discover(t) \/ Duplicate(t) \/ Start(t) \/
                          ObserveHTTP(t) \/ CancelPending(t) \/
                          CancelHTTP(t) \/ Submit(t) \/ Apply(t))
        \/ Stop \/ JoinCollector \/ JoinWorkers \/ Return

TypeOK ==
    /\ phase \in [Targets -> Phases]
    /\ requestCount \in [Targets -> 0..2]
    /\ outcome \in [Targets -> Outcomes]
    /\ reducerOutcome \in [Targets -> Outcomes]
    /\ \A s \in {admitted, duplicates, observed, submitted, receipts,
                 applied, dropped} : s \subseteq Targets
    /\ cancelled \in BOOLEAN /\ collectorJoined \in BOOLEAN
    /\ workersJoined \in BOOLEAN /\ returned \in BOOLEAN

RequestsAtMostOnce == \A t \in Targets : requestCount[t] <= 1
ObservedResultsRetained == observed \cap dropped = {}
ReturnWaitsForJoin == returned => (collectorJoined /\ workersJoined)
ReturnedObservationsApplied == returned =>
    (observed \subseteq applied /\
     \A t \in observed : reducerOutcome[t] = "responded")

(* Negated witnesses: TLC must violate each to prove useful paths reachable. *)
NeverCompletedWitness == ~(returned /\ ~cancelled /\ admitted = Targets /\
                       observed = Targets /\ applied = Targets /\ duplicates # {})
NeverCancelledObservationWitness == ~(returned /\ cancelled /\ observed # {} /\
                                  observed \subseteq applied /\
                                  collectorJoined /\ workersJoined)

Spec == Init /\ [][Next]_vars
=============================================================================
