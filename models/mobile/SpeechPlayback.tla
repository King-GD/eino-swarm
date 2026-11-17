-------------------------- MODULE SpeechPlayback ---------------------------
EXTENDS Naturals, Sequences

(* C-048: phone-speech.usePhoneSpeechSync/cancelPhoneSpeech and the shared
   SpeechPlayback.play/playPCM. Dispatch/Ready/Receive model one segment's HTTP
   request and ordered PCM frames. EndSource is the AudioBufferSource onended
   callback; Advance is the awaited segment loop. CatalogRefresh is the React
   provider-revision effect, separate from actual speech authority revocation.
   RefreshCancelsPaired restores the reproduced mid-playback cancellation.
   Fair progress assumes synthesis and scheduled sources eventually return/end;
   the model proves no successful partial completion, not model pronunciation,
   arbitrary provider availability, hardware audio or an unbounded answer. *)

CONSTANTS
    RefreshCancelsPaired,
    AdvanceBeforeDrain,
    DropLastSegment,
    IgnoreAuthorityLoss,
    Segments,
    Samples,
    RefreshBudget

VARIABLES
    mode, phase, part, requested, received, queued, played,
    authorityValid, cause, refreshes, refreshAt, cancelledWithQueue

vars == <<mode, phase, part, requested, received, queued, played,
          authorityValid, cause, refreshes, refreshAt, cancelledWithQueue>>

Active == {"dispatch", "waiting", "receiving", "draining"}
Terminal == {"complete", "cancelled", "failed"}
AllSamples == [i \in 1..(Segments * Samples) |-> i]
AllParts == [i \in 1..Segments |-> i]

Init ==
    /\ mode \in {"paired", "standalone"}
    /\ phase = "dispatch" /\ part = 1
    /\ requested = <<>> /\ received = 0 /\ queued = <<>> /\ played = <<>>
    /\ authorityValid = TRUE /\ cause = "none"
    /\ refreshes = 0 /\ refreshAt = {} /\ cancelledWithQueue = FALSE

Dispatch ==
    /\ phase = "dispatch" /\ authorityValid
    /\ requested' = Append(requested, part)
    /\ phase' = "waiting"
    /\ UNCHANGED <<mode, part, received, queued, played, authorityValid, cause,
                    refreshes, refreshAt, cancelledWithQueue>>

Ready ==
    /\ phase = "waiting"
    /\ phase' = "receiving"
    /\ UNCHANGED <<mode, part, requested, received, queued, played, authorityValid,
                    cause, refreshes, refreshAt, cancelledWithQueue>>

Receive ==
    /\ phase = "receiving" /\ received < Samples
    /\ queued' = Append(queued, (part - 1) * Samples + received + 1)
    /\ received' = received + 1
    /\ phase' = IF received + 1 = Samples THEN "draining" ELSE "receiving"
    /\ UNCHANGED <<mode, part, requested, played, authorityValid, cause,
                    refreshes, refreshAt, cancelledWithQueue>>

EndSource ==
    /\ phase \in {"receiving", "draining"} /\ queued /= <<>>
    /\ played' = Append(played, Head(queued))
    /\ queued' = Tail(queued)
    /\ UNCHANGED <<mode, phase, part, requested, received, authorityValid, cause,
                    refreshes, refreshAt, cancelledWithQueue>>

Advance ==
    /\ phase = "draining" /\ (queued = <<>> \/ AdvanceBeforeDrain)
    /\ IF part = Segments \/ (DropLastSegment /\ part = Segments - 1)
       THEN /\ phase' = "complete" /\ UNCHANGED <<part, received>>
       ELSE /\ phase' = "dispatch" /\ part' = part + 1 /\ received' = 0
    /\ UNCHANGED <<mode, requested, queued, played, authorityValid, cause,
                    refreshes, refreshAt, cancelledWithQueue>>

CatalogRefresh ==
    /\ phase \in Active /\ refreshes < RefreshBudget
    /\ refreshes' = refreshes + 1
    /\ refreshAt' = refreshAt \cup {phase}
                     \cup (IF queued /= <<>> THEN {"queued"} ELSE {})
                     \cup (IF part > 1 /\ phase = "dispatch" THEN {"between"} ELSE {})
    /\ IF mode = "standalone" \/ RefreshCancelsPaired
       THEN /\ phase' = "cancelled" /\ queued' = <<>> /\ cause' = "catalog"
       ELSE UNCHANGED <<phase, queued, cause>>
    /\ UNCHANGED <<mode, part, requested, received, played, authorityValid,
                    cancelledWithQueue>>

RevokeAuthority ==
    /\ phase \in Active /\ authorityValid
    /\ authorityValid' = FALSE
    /\ IF IgnoreAuthorityLoss
       THEN UNCHANGED <<phase, queued, cause, cancelledWithQueue>>
       ELSE /\ phase' = "cancelled" /\ queued' = <<>> /\ cause' = "revoked"
            /\ cancelledWithQueue' = (queued /= <<>>)
    /\ UNCHANGED <<mode, part, requested, received, played, refreshes, refreshAt>>

HumanStop ==
    /\ phase \in Active
    /\ phase' = "cancelled" /\ cause' = "human" /\ queued' = <<>>
    /\ cancelledWithQueue' = (queued /= <<>>)
    /\ UNCHANGED <<mode, part, requested, received, played, authorityValid,
                    refreshes, refreshAt>>

SynthesisFails ==
    /\ phase \in {"waiting", "receiving"}
    /\ phase' = "failed" /\ queued' = <<>> /\ cause' = "model"
    /\ UNCHANGED <<mode, part, requested, received, played, authorityValid,
                    refreshes, refreshAt, cancelledWithQueue>>

Progress == Dispatch \/ Ready \/ Receive \/ EndSource \/ Advance
Next == Progress \/ CatalogRefresh \/ RevokeAuthority \/ HumanStop \/ SynthesisFails
Spec == Init /\ [][Next]_vars /\ WF_vars(Progress)

TypeOK ==
    /\ mode \in {"paired", "standalone"} /\ phase \in Active \cup Terminal
    /\ part \in 1..Segments /\ received \in 0..Samples
    /\ requested \in Seq(1..Segments) /\ Len(requested) <= Segments
    /\ queued \in Seq(1..(Segments * Samples)) /\ Len(queued) <= Segments * Samples
    /\ played \in Seq(1..(Segments * Samples)) /\ Len(played) <= Segments * Samples
    /\ authorityValid \in BOOLEAN /\ cancelledWithQueue \in BOOLEAN
    /\ cause \in {"none", "catalog", "human", "revoked", "model"}
    /\ refreshes \in 0..RefreshBudget
    /\ refreshAt \subseteq Active \cup {"queued", "between"}

PairedRefreshKeepsOwner == mode = "paired" => cause /= "catalog"
NextPartWaitsForDrain == \A i \in 1..Len(queued) : queued[i] > (part - 1) * Samples
SuccessIncludesAllAudio == phase = "complete" =>
    (requested = AllParts /\ played = AllSamples /\ queued = <<>>)
AuthorityLossStopsAudio == ~authorityValid => (phase = "cancelled" /\ queued = <<>>)
PlaybackEventuallySettles == <>(phase \in Terminal)

NeverCompleteAfterRefresh == ~(mode = "paired" /\ phase = "complete" /\ refreshes > 0)
NeverRefreshWithQueuedAudio == ~(mode = "paired" /\ "queued" \in refreshAt)
NeverRefreshBetweenParts == ~(mode = "paired" /\ "between" \in refreshAt)
NeverRefreshWhileWaiting == ~(mode = "paired" /\ "waiting" \in refreshAt)
NeverStandaloneCatalogCancel == ~(mode = "standalone" /\ cause = "catalog")
NeverCancelQueuedAudio == ~cancelledWithQueue

=============================================================================
