---------------------- MODULE ScheduleAnswerAdmission ----------------------
EXTENDS Naturals

(* One private answer and its optional same-turn correction. A candidate can
   contain both an external observation and a concrete app scheduling claim.
   The external observation persists across the correction; the local claim
   can change. Each evidence category represents matching rows observed at
   verification. Ambiguity covers unqualified claims with multiple matches;
   an explicit reference is represented by one selected row. That row can
   change before the publication commit.

   "grounded" means the semantic extractor supplied an external label and
   the decoder found its exact witness and passage in the answer, with no
   app schedule ID in the structured item. TLC does not infer the label
   from prose. TreatExternalAsLocal models the observed extractor mistake:
   the external cadence is sent to app schedule matching. RequireExternalNew
   models the observed decoder refusal when the extractor omitted "new".
   Progress assumes a live owner, healthy storage and fair Verify/Commit. *)

CONSTANTS
    TreatExternalAsLocal, ExternalSkipsLocal, TrustInvalidExternal, SkipCommitRecheck,
    RequireExternalNew, PermitMissingLocalNew

ExternalInputs == {"absent", "grounded-with-new", "grounded-no-new", "missing-witness", "local-id"}
LocalClaims == {"absent", "existing", "new", "missing-new"}
Matches == {"none", "wrong-row", "ambiguous", "unique"}
Phases == {"private", "checked", "invalid", "correct", "error", "published"}
Codes == {"none", "no_matching_schedule", "schedule_state_changed"}

VARIABLES external, local, matches, receipt, stale, attempt, phase, code
vars == <<external, local, matches, receipt, stale, attempt, phase, code>>

GroundedExternal == external \in {"grounded-with-new", "grounded-no-new"}
ValidExternal == external = "absent" \/ GroundedExternal
LocalEvidence == matches = "unique" /\ (local # "new" \/ receipt)
RequiresLocalMatch ==
    \/ TreatExternalAsLocal /\ GroundedExternal
    \/ local # "absent" /\ ~(ExternalSkipsLocal /\ GroundedExternal)

Init ==
    /\ external \in ExternalInputs
    /\ local \in LocalClaims
    /\ matches \in Matches
    /\ receipt \in BOOLEAN
    /\ stale = FALSE
    /\ attempt = 1
    /\ phase = "private"
    /\ code = "none"

Refuse(reason) ==
    /\ phase' = IF attempt = 1 THEN "correct" ELSE "error"
    /\ code' = reason

Verify ==
    /\ phase = "private"
    /\ IF (~ValidExternal /\ ~TrustInvalidExternal)
           \/ (RequireExternalNew /\ external = "grounded-no-new")
           \/ (local = "missing-new" /\ ~PermitMissingLocalNew)
       THEN /\ phase' = "invalid" /\ UNCHANGED code
       ELSE IF RequiresLocalMatch /\ ~LocalEvidence
            THEN Refuse("no_matching_schedule")
            ELSE /\ phase' = "checked" /\ UNCHANGED code
    /\ UNCHANGED <<external, local, matches, receipt, stale, attempt>>

ChangeSelectedRow ==
    /\ phase = "checked" /\ matches = "unique" /\ ~stale
    /\ stale' = TRUE
    /\ UNCHANGED <<external, local, matches, receipt, attempt, phase, code>>

Commit ==
    /\ phase = "checked"
    /\ IF RequiresLocalMatch /\ stale /\ ~SkipCommitRecheck
       THEN Refuse("schedule_state_changed")
       ELSE /\ phase' = "published" /\ UNCHANGED code
    /\ UNCHANGED <<external, local, matches, receipt, stale, attempt>>

Correct ==
    /\ phase = "correct"
    /\ attempt = 1 /\ attempt' = 2
    /\ phase' = "private" /\ code' = "none"
    /\ local' \in LocalClaims
    /\ IF stale
       THEN /\ matches' \in Matches /\ receipt' \in BOOLEAN
       ELSE UNCHANGED <<matches, receipt>>
    /\ stale' = FALSE
    /\ UNCHANGED external

Next == Verify \/ ChangeSelectedRow \/ Commit \/ Correct
Spec == Init /\ [][Next]_vars /\ WF_vars(Verify) /\ WF_vars(Commit)

TypeOK ==
    /\ external \in ExternalInputs /\ local \in LocalClaims
    /\ matches \in Matches /\ receipt \in BOOLEAN /\ stale \in BOOLEAN
    /\ attempt \in 1..2 /\ phase \in Phases /\ code \in Codes

ExternalOnlyAvoidsCorrection ==
    GroundedExternal /\ local = "absent" => phase # "correct"

ExternalOnlyDoesNotFail ==
    GroundedExternal /\ local = "absent" => phase # "error"

ExternalDoesNotRequireNew ==
    external = "grounded-no-new" /\ local # "missing-new" => phase # "invalid"

LocalPublicationRequiresEvidence ==
    phase = "published" /\ local # "absent" =>
        matches = "unique" /\ (local = "existing" \/ receipt)

MalformedExternalStaysPrivate ==
    phase = "published" => external \notin {"missing-witness", "local-id"}

MissingLocalNewStaysPrivate ==
    phase = "published" => local # "missing-new"

PublishedLocalUsesCurrentRow ==
    phase = "published" /\ local # "absent" => ~stale

ExternalOnlyEventuallyPublishes ==
    (phase = "private" /\ GroundedExternal /\ local = "absent")
        ~> (phase = "published")

NeverExternalPublishedWithoutSchedule ==
    ~(phase = "published" /\ external = "grounded-no-new" /\ local = "absent"
      /\ matches = "none" /\ attempt = 1)

NeverCorrectedMixedPublished ==
    ~(phase = "published" /\ GroundedExternal /\ local = "absent"
      /\ matches = "none" /\ attempt = 2)

NeverVerifiedMixedPublished ==
    ~(phase = "published" /\ external = "grounded-no-new" /\ local = "new" /\ attempt = 1)

NeverStaleLocalRefused ==
    ~(phase = "correct" /\ GroundedExternal /\ local = "new"
      /\ code = "schedule_state_changed")

NeverRepeatedMixedFailed ==
    ~(phase = "error" /\ GroundedExternal /\ local = "new"
      /\ matches = "none" /\ code = "no_matching_schedule" /\ attempt = 2)

=============================================================================
