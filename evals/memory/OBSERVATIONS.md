# First mechanism-control observations

The continuation below supersedes the initial 300-second probe as the latest
effect-validation status. Older observations and raw attempts remain retained;
later successes do not erase their failures or unknown usage.

Status: calibration only. No recall/Dream product adoption. The historical trial
below is complete; it did not establish general task effectiveness.

Source baseline: `0007c4c32c8ee85a2790f13e63a67e6251b6597b`. All arms used the
same engine binary and configured provider/model, fresh private data and the same
shared evaluation instruction. Two synthetic cases, one attempt per arm, no
repeat-based uncertainty estimate.

| Arm | Attempts | Total provider-reported tokens | Relative to baseline |
|---|---:|---:|---:|
| baseline | 2 | 322,876 | +0.0% |
| recall | 2 | 400,677 | +24.1% |
| dream | 2 | 271,830 | -15.8% |
| combined | 2 | 247,815 | -23.2% |

Totals include Dream and final durable auxiliary calls after engine shutdown.
All eight attempts have complete cost and cleanup receipts, zero infrastructure
errors. These paired sums are descriptive; model variability and two artificial
cases cannot establish a stable improvement.

All arms passed the directory-policy control. All arms failed the exact service
label check in the incident-report control. Independent rubric audit found that
“billing service” is faithful to the supplied notes, while the expected “billing”
had no specified canonicalization rule. Original failures remain unchanged and
are not interpreted as demonstrated task errors. Do not quote 1/2 as a quality
score or silently rescore it as success.

Actual Trace confirms that the baseline found the skill outside the first 50
entries through existing discovery, then opened it with skill_view. The recall
arm executed the existing Engine MCP list/call proxy, recall_memory and skill_view.
Dream produced evidence-linked corrections of the stale directory policy;
structural acceptance does not establish semantic correctness in other tasks.

Disk-only current-use metadata found 9 project skill directories, including two
with 52 and 109 skills. Read-only current configuration inspection confirmed the
index limit of 50. This establishes a scope where the cutoff can matter; it does
not prove running-app behavior or a benefit from this candidate. No names, IDs
or memory contents were retained in that aggregate.

Evidence lives privately under:
`~/.codex/artifacts/memory-recall-dream-20261110/real-controls-v1/`.
Inspect manifest.json, runs/*/repeat-1/*/result.json, traces-post-shutdown.json,
Dream raw input/output and RUBRIC_AUDIT.json. The genuine historical R05 trial
uses a separate frozen fixture and independent parent-RED/fixed-GREEN scorer;
its results are reported below.

## Historical calibration admission failures

R05 v1 stopped before the target task: the nonstreaming Dream request received no
first byte within 30 seconds. The original failure, unknown billed usage and complete
cleanup remain recorded. A subsequent configured model catalog read returned 200.

R05 v2 separately froze a 120 second first-byte budget for all arms and 240 second
Dream deadline, leaving the 480 second task deadline unchanged. It received a fully
observed response after 100.607 seconds: 2691 input and 8192 output tokens, finish_reason
length, empty content and 33108 reasoning characters. The task did not start. The old
helper reported EOF; raw response and 10883 known tokens survived. No efficacy claim
or complete aggregate across the unknown-cost v1 is justified.

The next candidate revision reports the typed output-limit cause, frontloads the
generic no-update decision and keeps complete known usage separate from proposal
validity. A candidate failure can remain in the denominator without preventing
other arms from running. Frozen old artifacts and helper are retained; new rounds
use new helper/build/source receipts.

R05 v3's revised salience instruction produced a validated empty proposal in
4.044 seconds with 2943 known tokens. Before task start, the harness caught a helper
serialization defect: an empty notes slice encoded as null rather than the array
required by the interface. The round is retained as a harness failure with all
processes cleaned up. This is not a task-quality observation. A regression must
cover the actual empty-proposal model boundary before the next distinct round.

## Historical R05 v4 is an overhead negative control

The first attempted arm (Dream) reached the 24 manager-round measurement cap,
made no production delta and created no worker. The original independent target
test ran and failed. This is a real task failure under the stated measurement
budget; it is not an established memory-caused failure. That budget is 24 manager
rounds / 8192 output tokens per call, while source defaults are 200 / 16384.

The Dream proposal was empty, so task memory was identical to baseline plus
preprocessing cost. Known reported usage is 821353 task tokens plus 2885 Dream tokens
(824238 total). The original harness cost_complete:false is retained. A separate
read-only audit found its only incomplete-cost gate was waiting for a memory
review the product intentionally skips after cancellation. A new accounting
revision accepts known final usage only after process/auxiliary join evidence;
the original result is never rewritten.

Do not expand more generic bugfix tasks as a substitute for memory transfer.
Authentic history inspection found earlier wrong MUSL/search notes had already
been repaired before later tasks, so those wrong snapshots cannot be seeded as
baseline. One exact historical notes checkpoint is available, but its complete
historical skill catalog is not. These admission limits prevent false gains.

## Authentic current-memory probe

A complete current checkpoint was frozen before defining a new evaluation task:
31 notes, 2192/2200 characters and 109 skills. An earlier original human request
supplied a durable review preference. This is current-memory curation, not a
historical production-task replay; absence of an equivalent note does not prove
that the whole application forgot the preference or identify why it disappeared.

An independent private-copy mock Engine verified every note and full skill through
the actual Go HTTP reader: zero model calls, zero turns and no usage writes.
Actual Chat Completions and Responses canary tests prove that skill descriptions
and bodies remain local for overlap validation. The real writer receives only
the notes, authenticated original human record, total cap and per-note cap.

The single real probe reached its frozen 300-second deadline without a valid
proposal. Provider usage was absent, so cost is unknown, not zero. The helper
exited, all 31 original notes remained unchanged and no target task started.
This is a provider/transport deadline, not an observed capacity refusal or a
retention-quality result. No additional controller attempt or budget increase
was made. Later transport diagnostics showed that one helper callback did not
prove one upstream POST; the original probe's upstream attempt count is unknown.

The probe used a newly identified helper. Review had first reproduced and fixed
local-catalog escaping that changed five current descriptions; every newly
seeded skill is now read back through Store and compared exactly. Old rounds
remain immutable. Their 51 synthetic and 28 historical description scalars did
not contain the affected escapes. The Python evidence saver also now redacts
string values before JSON encoding, including escaped and short credentials.

## Adoption decision

Keep recall and Dream opt-in. Neither has met the heldout task-quality and
total-cost gate. Synthetic token differences are only calibration signals;
timeouts with unknown usage prevent a complete aggregate across all attempts.
Do not add autonomous Dream scheduling, enlarge always-injected memory or build
a wiki/vector store from these results. A later adoption trial needs a frozen
cohort where authentic cross-session knowledge matters, complete as-of source
and memory provenance, independent task acceptance and repeat-based uncertainty.
The present work establishes the measurement path and its limits, not that trial.

## Existing capacity advice contradicted the write boundary

The current manager prompt said that at 80% capacity every increasing write
would be refused and only reductions could land. The actual Store and memory
tool enforce total and per-entry limits instead. At 80/100 characters, adding
18 characters plus the two-character separator succeeds at exactly 100/100;
a replacement may also grow within the cap.

The correction keeps the same storage policy and turn-start prompt snapshot,
reports actual free capacity, and preserves unrelated notes during consolidation.
Unit tests cover fitting growth, actual overflow, per-entry limits and warning
boundaries. An actual Engine → ADK → provider HTTP regression proves that the
model sees the corrected advice and receives the real successful tool receipt
on its next request. This is a correctness fix, not a measured Dream benefit.

## Continuation: real writer and reader effects

The original timeout was investigated rather than treated as an efficacy result.
The experimental helper could silently issue a second HTTP request through the
ordinary header-timeout recovery path; its auxiliary builder could also retry
without an explicitly configured reasoning effort. One model callback therefore
did not prove one upstream request. The helper now uses isolated recovery and
the explicit model builder. Real loopback red/green tests prove one POST for
header failure and for rejection of an explicit effort; ordinary product
recovery is unchanged. All later actual POSTs are independently metered without
logging authorization, upstream addresses or request bodies. Missing usage stays
unknown, including calls cancelled by the measurement harness.

An explicit low-effort streaming request over the same frozen notes returned a
proposal in 56.46 seconds, with 6175 reported tokens. The corresponding default
effort request exhausted its 4096 output tokens on reasoning without a proposal.
This identifies a useful diagnostic/configuration difference, not a general
model-speed result. Independent review rejected the low-effort proposal because
its condensed review preference omitted an explicit success condition. A repair
attempt also exhausted its output budget; neither candidate was adopted.

A separate hierarchical candidate gave the writer only catalog names and
descriptions, the complete notes and one original human source. It moved one
model-selected procedural note losslessly into a new skill and added the
source-supported review requirement. The actual Store applier retained all
original note text and all 109 existing skills. Notes changed from 2192/2200 to
2153/2200 characters; the new skill ranked outside the first 50. The writer took
46.53 seconds and 13758 tokens. This proves a valid private-copy curation, not
that later agents can discover or use the moved procedure.

Controlled reader tasks used the real Engine, the same full privacy-projected
catalog, original human source and isolated data in both arms. These are derived
component tasks, not unchanged historical production replays. An initial code
task baseline completed with independently accepted self-review at 206740 tokens;
the curated arm produced working code but timed out, with at least 458682 known
tokens and incomplete cost. A fresh larger-budget baseline also timed out after
producing working code. These outcomes do not establish a memory-caused code
regression or a performance benefit; no further timeout increase is used to
turn them into successes.

A targeted local stdout-retention task completed correctly in both arms. Its
simulated producer, all 4153344 output bytes, exit status and durable receipts
were independently checked. Reader totals were 115024 baseline versus 161411
curated tokens, about 40.3% higher for the curated arm. Both foreground tasks
opened zero skills, so this task cannot establish discovery of the moved skill.
More seriously, the curated arm's actual post-turn review edited that skill and
introduced a rule unsupported by the successful tool results, while losing part
of the original procedure. The baseline preserved its memory. All original 109
skills survived, but the newly promoted source did not. Foreground task success
must therefore be evaluated separately from post-turn memory preservation.

Three private write-before-verification probes used a generic evidence/scope
checker: it rejected that actual bad patch, allowed the actual human preference
addition and allowed a synthetic, explicitly authorized parameter replacement.
The three decisions matched their independent binary rubrics, costing 21694
tokens and 120.065 seconds combined. The bad-patch judgment nevertheless marked
existing-content preservation true and missed part of the retention risk. Three
selected cases, including one synthetic case, establish neither general
reliability nor a false-refusal rate. This checker is not a default product gate;
ordinary model-authored skill patch semantics remain a known limitation.

Private raw evidence is under `provider-diagnostic-v1`, `reader-effect-v1`,
`reader-effect-v2-code`, `reader-ssh-retention-v1`,
`independent-memory-verifier-probe-v1`,
`independent-memory-verifier-probe-v2-synthetic-positive` and
`effect-oracle-audit-v1` beneath the artifact root above. Original trials,
grading ambiguities, failed attempts and unknown costs remain unchanged.

## Query-index component trial

Before adopting either maintenance or retrieval changes, retain the separate
index-component result: one frozen, post-selected read-only task compared the
stock index with a query-ranked 50-entry index over identical complete memory.
The stock agent did discover and open the target procedure through existing
catalog fallback, at 128.94 seconds. It wrote a numerically correct report but
was interrupted at the 360-second task budget before its final answer. The
query-index candidate completed its turn in 275.15 seconds. Both original
mechanical results were false; all numeric, generation and source-count fields
were correct, while risk labels on pending items require scope/enum review.
Neither the cutoff nor a canonical label alone establishes a quality gain.
The candidate opened the target's full body at 18.28 seconds, an encouraging
single-pair discovery signal. Its actual computation kept timestamp
classification inside the done-only branch, but added a separate unsupported
inference from a pending row's imported count to possible risk. The fixture's
risk enums lack exclusive definitions, so neither this extra inference nor the
raw mismatch is presented as a quantified quality regression. Earlier retrieval
is not evidence of correct application or a repeat-based efficiency gain.

The stock ledger has 15 actual POSTs and 302984 known tokens; the candidate has
12 POSTs and 190633 known tokens. Both contain missing usage, so these are
partial ledgers, not complete token savings or monetary-cost estimates. The
candidate preserved all notes and skills exactly, and both owned cleanup and
meter shutdown completed. Only a quality supplement was allowed after binding
the stock's unknown calls to actual local interruption/deadline evidence; the
old row was neither rerun nor rescored. This single selected component trial
does not justify default query ranking, general speed claims or adoption.
See `index-reader-effect-v1`, `index-reader-effect-quality-v2` and
`late-procedure-discovery-admission-v1` in the private artifact root.

## Deterministic source retention repair

The investigation reproduced a separate current implementation defect:
deterministic family folding used token overlap to decide that the keeper
already retained a source, then deleted that source. Actual Store regressions
failed for negation, numbers, conditions and ordering. Complete-containment and
whitespace-normalization controls already passed. The fix omits a source body
only when its complete normalized text occurs in the original keeper. It does
not infer semantic equivalence from similarity or deduplicate two newly added
source sections against each other.

Failure injection also showed that a successful keeper write or partial source
deletion could disappear from the fold report and actual memory-review event.
The Store now returns those actual changes alongside the original error, and
the real Engine finalizer records them before handling the error. Usage-write
failures and partial removal with still-readable/already-removed sources are
covered, including retry without double counting. Filesystem operations remain
nontransactional. Manual merge APIs are unchanged; empty-body derivation shares
the same corrected preservation helper.

All original loss/partial-receipt assertions were observed red before the fix
and green afterward. The full memory package passed with the race detector at
98.6% statement coverage; related Engine fold/merge/tidy/finalize regressions,
vet and changed-line lint passed. Modified functions exceed 90% coverage. This
is a verified retention-correctness improvement, not evidence that autonomous
Dream improves general task success or that semantic model patching is solved.

A zero-model actual-Store stock/fixed replay over the complete current curated
checkpoint found zero foldable families: both retained all 31 notes and 110
skills exactly, with no writes or errors. All seeded usage counters were zero,
so this replay is not evidence of positive usage transfer. It confirms no change
for this current catalog; the repair's demonstrated benefit is preventing the
losses reproduced by the red/green cases. See
`current-catalog-fold-validation-v1/SUMMARY.json` in the artifact root.

## Review browser fixture validation

The first targeted browser run timed out while a new foreground turn was
running during a deliberately held old manual review. A separate instrumented
run reproduced it with the unchanged 60-second assertion and linked actual
HTTP framing to durable Trace: a nonstreaming composer-prediction request got
SSE from the keyless fixture, producing a JSON decode error and origin backoff.
The held manual review took the recovery probe, so the new manager waited at
the same origin gate without reaching the fixture. This was a test response
framing defect, with no evidence implicating fold, capacity advice or review
locking. The ordinary fixture branch now sends JSON for nonstreaming requests
and SSE for streaming requests. Actual HTTP red/green coverage and both targeted
browser cases passed after that change, without changing product backoff or the
wait budget. The original failures and diagnostic records are retained.
