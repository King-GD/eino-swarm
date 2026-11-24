---------------------------- MODULE PhoneRoster ----------------------------
EXTENDS Naturals, FiniteSets

(* Phone running roster after a sub-agent's lifetime ends. The watchdog
   stores one finished event; the phone treats that as failed, not as a
   parent error. error and workers_retained do not retire or revive a
   worker. cleanup cancels only workers the phone still shows as running.
   A later spawned is one new run. Sidecar rows stay outside the history
   cursor. This is the projection, not the watchdog timer or manager stop. *)

CONSTANTS
    IgnoreTimeout,
    ErrorKillsRunning,
    RetainRevives,
    CountEachSpawn,
    ReadyMovesCursor

MaxSteps == 6

VARIABLES
    step, seq, pageLo, cursor,
    phase, cause, died, gen, deathGen, spawnSeq, entries, exposed

vars == <<step, seq, pageLo, cursor, phase, cause, died, gen, deathGen, spawnSeq, entries, exposed>>

Workers == {"a", "b"}

OffPage(w) == phase[w] = "running" /\ spawnSeq[w] > 0 /\ spawnSeq[w] < pageLo

InPage(w) == phase[w] = "running" /\ spawnSeq[w] >= pageLo

Shown(w) ==
    IF InPage(w) THEN 1
    ELSE IF OffPage(w) THEN (IF CountEachSpawn THEN entries[w] ELSE 1)
    ELSE 0

Button == Shown("a") + Shown("b")

RunningN == Cardinality({w \in Workers : phase[w] = "running"})

OffPageSeqs == {spawnSeq[w] : w \in {x \in Workers : OffPage(x)}}

Init ==
    /\ step = 0
    /\ seq = 1
    /\ pageLo = 5
    /\ cursor = 5
    /\ phase = [w \in Workers |-> "absent"]
    /\ cause = [w \in Workers |-> "none"]
    /\ died = [w \in Workers |-> FALSE]
    /\ gen = [w \in Workers |-> 0]
    /\ deathGen = [w \in Workers |-> 0]
    /\ spawnSeq = [w \in Workers |-> 0]
    /\ entries = [w \in Workers |-> 0]
    /\ exposed = [w \in Workers |-> FALSE]

Spawn(w) ==
    /\ step < MaxSteps
    /\ seq <= MaxSteps
    /\ phase[w] \in {"absent", "failed", "done", "cancelled"}
    /\ phase' = [phase EXCEPT ![w] = "running"]
    /\ cause' = [cause EXCEPT ![w] = "none"]
    /\ gen' = [gen EXCEPT ![w] = gen[w] + 1]
    /\ spawnSeq' = [spawnSeq EXCEPT ![w] = seq]
    /\ entries' = [entries EXCEPT ![w] = entries[w] + 1]
    /\ seq' = seq + 1
    /\ step' = step + 1
    /\ UNCHANGED <<pageLo, cursor, died, deathGen, exposed>>

Timeout(w) ==
    /\ step < MaxSteps
    /\ seq <= MaxSteps
    /\ phase[w] = "running"
    /\ died' = [died EXCEPT ![w] = TRUE]
    /\ deathGen' = [deathGen EXCEPT ![w] = gen[w]]
    /\ cause' = [cause EXCEPT ![w] = "timeout"]
    /\ IF IgnoreTimeout
       THEN UNCHANGED phase
       ELSE phase' = [phase EXCEPT ![w] = "failed"]
    /\ seq' = seq + 1
    /\ step' = step + 1
    /\ UNCHANGED <<pageLo, cursor, gen, spawnSeq, entries, exposed>>

FinishOK(w) ==
    /\ step < MaxSteps
    /\ seq <= MaxSteps
    /\ phase[w] = "running"
    /\ cause[w] # "timeout"
    /\ phase' = [phase EXCEPT ![w] = "done"]
    /\ cause' = [cause EXCEPT ![w] = "ok"]
    /\ seq' = seq + 1
    /\ step' = step + 1
    /\ UNCHANGED <<pageLo, cursor, died, gen, deathGen, spawnSeq, entries, exposed>>

ParentError ==
    /\ step < MaxSteps
    /\ exposed' = [w \in Workers |->
        exposed[w] \/ (phase[w] = "running" /\ cause[w] # "timeout")]
    /\ phase' = [w \in Workers |->
        IF ErrorKillsRunning /\ phase[w] = "running" /\ cause[w] # "timeout"
        THEN "failed"
        ELSE phase[w]]
    /\ step' = step + 1
    /\ UNCHANGED <<seq, pageLo, cursor, cause, died, gen, deathGen, spawnSeq, entries>>

Cleanup ==
    /\ step < MaxSteps
    /\ phase' = [w \in Workers |-> IF phase[w] = "running" THEN "cancelled" ELSE phase[w]]
    /\ cause' = [w \in Workers |-> IF phase[w] = "running" THEN "cancel" ELSE cause[w]]
    /\ step' = step + 1
    /\ UNCHANGED <<seq, pageLo, cursor, died, gen, deathGen, spawnSeq, entries, exposed>>

Retain ==
    /\ step < MaxSteps
    /\ IF RetainRevives
       THEN phase' = [w \in Workers |->
            IF died[w] /\ deathGen[w] = gen[w] /\ phase[w] = "failed"
            THEN "running"
            ELSE phase[w]]
       ELSE UNCHANGED phase
    /\ step' = step + 1
    /\ UNCHANGED <<seq, pageLo, cursor, cause, died, gen, deathGen, spawnSeq, entries, exposed>>

Ready ==
    /\ step < MaxSteps
    /\ IF ReadyMovesCursor /\ OffPageSeqs # {}
       THEN cursor' = CHOOSE s \in OffPageSeqs : \A t \in OffPageSeqs : s <= t
       ELSE UNCHANGED cursor
    /\ step' = step + 1
    /\ UNCHANGED <<seq, pageLo, phase, cause, died, gen, deathGen, spawnSeq, entries, exposed>>

Earlier ==
    /\ step < MaxSteps
    /\ pageLo > 1
    /\ pageLo' = pageLo - 1
    /\ cursor' = pageLo'
    /\ step' = step + 1
    /\ UNCHANGED <<seq, phase, cause, died, gen, deathGen, spawnSeq, entries, exposed>>

Next ==
    \/ \E w \in Workers : Spawn(w) \/ Timeout(w) \/ FinishOK(w)
    \/ ParentError \/ Cleanup \/ Retain \/ Ready \/ Earlier

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ step \in 0..MaxSteps
    /\ seq \in 1..(MaxSteps + 1)
    /\ pageLo \in 1..5
    /\ cursor \in 1..MaxSteps
    /\ phase \in [Workers -> {"absent", "running", "failed", "done", "cancelled"}]
    /\ cause \in [Workers -> {"none", "timeout", "ok", "cancel"}]
    /\ died \in [Workers -> BOOLEAN]
    /\ exposed \in [Workers -> BOOLEAN]
    /\ gen \in [Workers -> 0..MaxSteps]
    /\ deathGen \in [Workers -> 0..MaxSteps]
    /\ spawnSeq \in [Workers -> 0..MaxSteps]
    /\ entries \in [Workers -> 0..MaxSteps]
    /\ \A w \in Workers : deathGen[w] <= gen[w]

\* The watchdog finish is the terminal phone state for that run.
DeadStaysDead ==
    \A w \in Workers :
        (died[w] /\ deathGen[w] = gen[w]) =>
            (phase[w] = "failed" /\ cause[w] = "timeout")

\* A parent error does not by itself end a worker the phone still has running.
ErrorDoesNotRetire ==
    \A w \in Workers :
        exposed[w] =>
            phase[w] = "running" \/ cause[w] \in {"timeout", "ok", "cancel"}

\* The running count is the set of workers, one row each.
CountAgrees == Button = RunningN

\* A ready snapshot does not pull the history cursor back to a sidecar spawn.
CursorStable == cursor = pageLo

NeverTimedOut == \A w \in Workers : ~died[w]

NeverPeerSurvives ==
    ~(\E a, b \in Workers :
        a /= b /\ died[a] /\ phase[a] = "failed" /\ phase[b] = "running" /\ ~died[b])

NeverRespawned == ~(\E w \in Workers : died[w] /\ phase[w] = "running")

NeverCursorHeld ==
    ~(\E w \in Workers :
        died[w] /\ phase[w] = "failed" /\ spawnSeq[w] < pageLo /\ cursor = pageLo)
=============================================================================
