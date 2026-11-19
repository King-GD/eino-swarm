----------------------------- MODULE ConcurrencyCap -----------------------------
EXTENDS Naturals, Sequences

(* One task's sub-agent slots. holders is oldest-first: the workers inside a
   model call. waiting is the programmatic queue. Lowering cap keeps the
   oldest holders and stops the newest until Len(holders) <= cap.
   ShrinkKeepsHolders is the previous gate, which left those holders running.
   SaveInvalid represents a PUT with an explicitly non-positive cap. The
   historical Settings adapter normalized it to the default and acknowledged
   the write. Refusal happens before config or worker admission changes.
   This is the admission protocol, not Go locking or tool cancellation time. *)

CONSTANTS
    MaxWorkers,
    ShrinkKeepsHolders,
    NormalizeInvalidSave

VARIABLES cap, nextID, holders, waiting, stopped, invalidSave

vars == <<cap, nextID, holders, waiting, stopped, invalidSave>>

TypeOK ==
    /\ cap \in 1..MaxWorkers
    /\ nextID \in 1..(MaxWorkers + 1)
    /\ holders \in Seq(1..MaxWorkers)
    /\ waiting \in Seq(1..MaxWorkers)
    /\ stopped \in 0..MaxWorkers
    /\ invalidSave \in {"none", "written", "refused"}
    /\ Len(holders) + Len(waiting) + stopped <= MaxWorkers

HoldersWithinCap == Len(holders) <= cap

NeverPreempted == stopped = 0

InvalidSaveRefused == invalidSave /= "written"

NeverRefused == invalidSave /= "refused"

DropAt(s, i) ==
    IF i = 1 THEN Tail(s)
    ELSE IF i = Len(s) THEN SubSeq(s, 1, i - 1)
    ELSE SubSeq(s, 1, i - 1) \o SubSeq(s, i + 1, Len(s))

Init ==
    /\ cap = MaxWorkers
    /\ nextID = 1
    /\ holders = <<>>
    /\ waiting = <<>>
    /\ stopped = 0
    /\ invalidSave = "none"

Queue ==
    /\ nextID <= MaxWorkers
    /\ waiting' = Append(waiting, nextID)
    /\ nextID' = nextID + 1
    /\ UNCHANGED <<cap, holders, stopped, invalidSave>>

Acquire ==
    /\ waiting /= <<>>
    /\ Len(holders) < cap
    /\ holders' = Append(holders, Head(waiting))
    /\ waiting' = Tail(waiting)
    /\ UNCHANGED <<cap, nextID, stopped, invalidSave>>

Finish ==
    /\ holders /= <<>>
    /\ \E i \in 1..Len(holders):
        holders' = DropAt(holders, i)
    /\ UNCHANGED <<cap, nextID, waiting, stopped, invalidSave>>

SetCap(n) ==
    /\ n \in 1..MaxWorkers
    /\ n /= cap
    /\ cap' = n
    /\ IF ShrinkKeepsHolders \/ Len(holders) <= n
       THEN UNCHANGED <<holders, stopped>>
       ELSE /\ holders' = SubSeq(holders, 1, n)
            /\ stopped' = stopped + Len(holders) - n
    /\ UNCHANGED <<nextID, waiting, invalidSave>>

SaveInvalid ==
    /\ invalidSave = "none"
    /\ IF NormalizeInvalidSave
       THEN /\ cap' = MaxWorkers
            /\ invalidSave' = "written"
       ELSE /\ UNCHANGED cap
            /\ invalidSave' = "refused"
    /\ UNCHANGED <<nextID, holders, waiting, stopped>>

Next ==
    \/ SaveInvalid
    \/ Queue
    \/ Acquire
    \/ Finish
    \/ \E n \in 1..MaxWorkers: SetCap(n)

Spec == Init /\ [][Next]_vars

=============================================================================
