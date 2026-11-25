# Memory recall and Dream experiment

Status: experimental; recall and Dream are not wired into production. This study follows
[The components of agent memory](https://www.anthropic.com/research/components-of-agent-memory).

A separate correctness fix makes the existing 80% pressure warning agree with
the existing Store limits. It does not enable either experimental candidate.

## Hypotheses and arms

A uses the current engine with notes, the existing bounded skill index, unrestricted
known-name `skill_view`, ordinary workspace tools, project history and automatic
post-turn review. B adds a read-only MCP tool that searches the complete recorded
skill catalog. C applies one offline Dream to a fresh notes copy. D combines B and C.
The same identified engine executable serves every arm. The helper is never loaded
by the ordinary app. Experimental code lives under `internal/memoryexperiment`.

Recall searches names and descriptions using lexical terms and CJK bigrams; it
returns applicability metadata, not skill bodies. Existing `skill_view` opens the
body and records use. This tests discoverability, not embedding quality. All arms
keep existing fallback discovery through files/history and arbitrary known names.
A common measurement instruction clarifies that discovered recorded names may be
opened; this is a documented deviation from an entirely untouched production prompt.

Dream receives only frozen pre-cutoff notes and original records, never the target
question, expected answers, future changes or hidden tests. Sources must belong to
one project and precede an exclusive cutoff. Citations identify `(thread_id, seq)`
and exact bytes. Human records and paired non-refused tool receipts may support a
proposal. Assistant claims and derived summaries are context, not evidence. Scope,
quotes, numeric spelling, exact old-note matching, capacity and revision checks
are mechanical gates; they do not establish semantic entailment. The candidate
changes only notes in its caller-selected private copy, never skills or live data.

## Freeze and execution

Before external measurement, freeze fixture/rubric, source revision, engine/helper
and harness hashes, chosen provider/model, limits, arm order and repetition count.
Calibration and heldout task families must be disjoint. Every attempt owns fresh
data, HOME/XDG/ZDOTDIR, workspace and memory. Sources are available to the baseline
through the actual project history tool. No shared mutable memory crosses arms.
Synthetic controls establish mechanism only. Derived historical transfers must
retain their source map and explicitly state differences from the original task.
A result on a reduced transfer does not accept the original complete bugfix.

Ordinary automated tests are offline. Real-model calls belong only to the explicit
opt-in measurement command, consistent with the existing `make eval` separation.
No native window, user engine, production store or external messaging is involved.
Private evidence is created with mode 0600 without changing subprocess umask.

## Acceptance and accounting

Primary outcomes are independent task acceptance, repeated errors, stale-memory
misuse and preservation of project/permission constraints. Retain all attempts,
including invalid output, timeout and provider failure; distinguish infrastructure
failures from task failures. Tool hits and model self-reports are not task success.

Count every actual provider call including Dream and post-turn review. Missing
usage means incomplete cost, never zero. Report raw and cached input, output,
total tokens and wall time; do not infer money from tokens without a price source.
A failed Dream stays a failed candidate attempt; do not silently fall back to A.
An observed output-limit or invalid-proposal failure with complete provider usage
is a candidate failure: retain its known Dream cost, record that no target task
started, and continue the other arms. Transport failure, unknown usage or uncertain
cleanup stops the schedule. Candidate success and cost completeness are separate.
The manifest labels this accounting revision; earlier rounds are never rescored.

For adoption, require no new critical error, quality no worse on heldout families,
and a meaningful paired improvement in task acceptance or repeated investigation,
with total cost and uncertainty reported. A small pilot can justify a larger
trial, not a general capability claim. Do not tune to heldout failures or exclude
unfavorable results. Product integration is a subsequent decision based on these
receipts, with its own runtime and native acceptance where applicable.
