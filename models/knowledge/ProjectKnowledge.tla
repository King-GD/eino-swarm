------------------------- MODULE ProjectKnowledge ---------------------------
EXTENDS Naturals

CONSTANTS MaxRevision, UseStaleOwner, IgnoreReadOnly, IgnoreRevision,
          PublishPartial, AllowForeignSource, ResolveByLocatorOnly, IgnoreContentChange,
          ReplaceSchema

VARIABLES library, turn, writerHere, history, request, receipt

vars == <<library, turn, writerHere, history, request, receipt>>

Operations == {"put", "delete", "append"}
TurnTools == {"none", "disabled", "read", "write"}
ArchivedIdentities == {"original", "edited", "replacement"}
Identities == ArchivedIdentities \cup {"missing"}
Types == {"base", "extra"}
Page == [bytes: {"absent", "complete", "partial"}, revision: 0..MaxRevision,
         source: Identities]
Library == [page: Page, schemaRevision: 0..MaxRevision, types: SUBSET Types,
            clock: 0..MaxRevision]
Request == [op: Operations \cup {"none"}, expected: 0..MaxRevision,
            capturedHere: BOOLEAN, cite: BOOLEAN]
Receipt == [outcome: {"none", "commit", "refuse", "read"}, op: Operations \cup {"none"},
            authorized: BOOLEAN, currentRevision: BOOLEAN, scopedSource: BOOLEAN,
            before: Library, wanted: Identities, found: Identities, available: BOOLEAN]

EmptyPage == [bytes |-> "absent", revision |-> 0, source |-> "missing"]
EmptyLibrary == [page |-> EmptyPage, schemaRevision |-> 0, types |-> {}, clock |-> 0]
NoRequest == [op |-> "none", expected |-> 0, capturedHere |-> FALSE, cite |-> FALSE]
NoReceipt == [outcome |-> "none", op |-> "none", authorized |-> TRUE,
              currentRevision |-> TRUE, scopedSource |-> TRUE, before |-> EmptyLibrary,
              wanted |-> "missing", found |-> "missing", available |-> FALSE]
HistoryRow == [phase: ArchivedIdentities \cup {"rewound"}, here: BOOLEAN]
Identity == IF history.phase = "rewound" THEN "missing" ELSE history.phase
Enabled == library.schemaRevision # 0

Init ==
    /\ library = EmptyLibrary /\ turn = "none" /\ writerHere = TRUE
    /\ history = [phase |-> "original", here |-> TRUE]
    /\ request = NoRequest /\ receipt = NoReceipt

Enable ==
    /\ ~Enabled
    /\ library' = [library EXCEPT !.schemaRevision = 1, !.types = {"base"}, !.clock = 1]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<turn, writerHere, history, request>>

BindTurn(readOnly) ==
    /\ turn = "none" /\ writerHere
    /\ turn' = IF ~Enabled THEN "disabled" ELSE IF readOnly THEN "read" ELSE "write"
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, writerHere, history, request>>

EndTurn ==
    /\ turn # "none"
    /\ turn' = "none"
    /\ request' = NoRequest /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, writerHere, history>>

MoveWriter ==
    /\ writerHere' = ~writerHere
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, turn, history, request>>

MoveSource ==
    /\ history' = [history EXCEPT !.here = ~@]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, turn, writerHere, request>>

AdvanceHistory ==
    /\ history.phase # "replacement"
    /\ history' = [history EXCEPT !.phase = IF @ = "rewound" THEN "replacement" ELSE "rewound"]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, turn, writerHere, request>>

EditSource ==
    /\ history.phase = "original"
    /\ history' = [history EXCEPT !.phase = "edited"]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, turn, writerHere, request>>

Revision(op) == IF op = "append" THEN library.schemaRevision ELSE library.page.revision
Eligible(op) ==
    CASE op = "delete" -> library.page.revision # 0
      [] op = "append" -> "extra" \notin library.types
      [] OTHER -> TRUE

(* Capture is preparation of an attempted tool call. It grants no permission.
   Read-only attempts model calls arriving at the Registry admission boundary. *)
Capture(op, cite) ==
    /\ turn \in {"read", "write"} /\ request = NoRequest
    /\ Eligible(op)
    /\ request' = [op |-> op, expected |-> Revision(op),
                   capturedHere |-> writerHere, cite |-> cite]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<library, turn, writerHere, history>>

(* A second authorized writer publishes complete new bytes using the current
   revision. The existing call still holds the earlier token. *)
CompetingWrite ==
    /\ turn = "write" /\ writerHere /\ request.op # "none" /\ library.clock < MaxRevision
    /\ library' = [library EXCEPT
                   !.clock = @ + 1,
                   !.page = [bytes |-> "complete", revision |-> library.clock + 1,
                             source |-> library.page.source]]
    /\ receipt' = NoReceipt
    /\ UNCHANGED <<turn, writerHere, history, request>>

Commit(op, cite) ==
    LET nextRevision == library.clock + 1
    IN CASE op = "append" -> [library EXCEPT !.clock = nextRevision,
                             !.schemaRevision = nextRevision,
                             !.types = IF ReplaceSchema THEN {"extra"} ELSE @ \cup {"extra"}]
         [] op = "delete" -> [library EXCEPT !.clock = nextRevision, !.page = EmptyPage]
         [] OTHER -> [library EXCEPT !.clock = nextRevision,
                         !.page = [bytes |-> "complete", revision |-> nextRevision,
                                   source |-> IF cite THEN Identity ELSE library.page.source]]

Finish(ioFailure) ==
    LET op == request.op
        fresh == request.expected = Revision(op)
        ownerOK == writerHere /\ turn = "write"
        sourceOK == ~request.cite \/ (history.here /\ Identity # "missing")
        authorized == (IF UseStaleOwner THEN request.capturedHere ELSE writerHere)
                      /\ (turn = "write" \/ (IgnoreReadOnly /\ turn = "read"))
        sourceAccepted == sourceOK \/ (AllowForeignSource /\ Identity # "missing")
        admitted == authorized /\ (fresh \/ IgnoreRevision) /\ sourceAccepted /\ Eligible(op)
        accepted == admitted /\ ~ioFailure
    IN /\ op # "none" /\ library.clock < MaxRevision
       /\ library' = IF accepted THEN Commit(op, request.cite)
                      ELSE IF admitted /\ ioFailure /\ PublishPartial /\ op = "put"
                           THEN [library EXCEPT !.page.bytes = "partial"]
                           ELSE library
       /\ receipt' = [NoReceipt EXCEPT !.outcome = IF accepted THEN "commit" ELSE "refuse",
                       !.op = op, !.authorized = ownerOK, !.currentRevision = fresh,
                       !.scopedSource = sourceOK, !.before = library]
       /\ request' = NoRequest
       /\ UNCHANGED <<turn, writerHere, history>>

(* A page read resolves the stored source against the history snapshot for
   this request. The locator stays fixed while the row identity can change. *)
ReadSource ==
    LET wanted == library.page.source
        found == IF history.here THEN Identity ELSE "missing"
        available == found # "missing" /\ (wanted = found \/
                        (ResolveByLocatorOnly /\ found = "replacement") \/
                        (IgnoreContentChange /\ wanted = "original" /\ found = "edited"))
    IN /\ library.page.bytes = "complete" /\ wanted # "missing"
       /\ receipt' = [NoReceipt EXCEPT !.outcome = "read", !.wanted = wanted,
                       !.found = found, !.available = available]
       /\ UNCHANGED <<library, turn, writerHere, history, request>>

Next ==
    \/ Enable \/ BindTurn(TRUE) \/ BindTurn(FALSE) \/ EndTurn
    \/ MoveWriter \/ MoveSource \/ AdvanceHistory \/ EditSource \/ CompetingWrite \/ ReadSource
    \/ \E op \in Operations : Capture(op, FALSE)
    \/ Capture("put", TRUE)
    \/ Finish(TRUE) \/ Finish(FALSE)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ library \in Library /\ turn \in TurnTools /\ writerHere \in BOOLEAN
    /\ history \in HistoryRow /\ request \in Request /\ receipt \in Receipt
    /\ ((library.page.bytes = "absent" <=> library.page.revision = 0)
         \/ library.page.bytes = "partial")

CurrentOwnerRequired == receipt.outcome = "commit" => receipt.authorized
RevisionRequired == receipt.outcome = "commit" => receipt.currentRevision
RefusalPreservesLibrary == receipt.outcome = "refuse" => library = receipt.before
SourceMustBeScoped == receipt.outcome = "commit" => receipt.scopedSource
OriginalIdentityRequired ==
    (receipt.outcome = "read" /\ receipt.available) => receipt.wanted = receipt.found
SchemaOnlyAppends ==
    (receipt.outcome = "commit" /\ receipt.op = "append") => receipt.before.types \subseteq library.types

NeverAttributedWrite ==
    ~(receipt.outcome = "commit" /\ receipt.op = "put" /\ library.page.source = "original")
NeverOriginalRead ==
    ~(receipt.outcome = "read" /\ receipt.available /\ receipt.wanted = "original")
NeverStaleRefusal ==
    ~(receipt.outcome = "refuse" /\ receipt.authorized /\ ~receipt.currentRevision)
NeverReadOnlyRefusal ==
    ~(receipt.outcome = "refuse" /\ turn = "read" /\ writerHere /\ receipt.currentRevision)
NeverReusedLocatorUnavailable ==
    ~(receipt.outcome = "read" /\ receipt.wanted = "original"
      /\ receipt.found = "replacement" /\ ~receipt.available)
NeverEditedSourceUnavailable ==
    ~(receipt.outcome = "read" /\ receipt.wanted = "original"
      /\ receipt.found = "edited" /\ ~receipt.available)
NeverSchemaAppend ==
    ~(receipt.outcome = "commit" /\ receipt.op = "append" /\ library.types = Types)

=============================================================================
