# Project knowledge publication and evidence

`ProjectKnowledge.tla` is a finite model of the write and historical-source
boundaries in `C-053`. TLC uses the pinned release in
`tools/model_check.py`. The standard `make model-check` target includes the
cases. To run this model alone and retain the configurations and traces:

```sh
evidence="$(mktemp -d)"
cases="$(python3 tools/model_check.py --list | awk '/^knowledge-/ {print $1}')"
printf '%s\n' "$cases" | xargs python3 tools/model_check.py --out "$evidence"
```

## Implementation mapping

| Model action or state | Implementation boundary |
| --- | --- |
| `Enable`, `BindTurn`, `turn` | `EnableProjectKnowledge` publishes the starter file. `snapshotProjectKnowledge` and `knowledgeTools` bind the enabled state and tool surface. Planned or read-only turns have read access. `Capture` represents an attempted call, including an attempted write that the Registry should refuse. |
| `MoveWriter`, authority check in `Finish` | `recordKnowledge` orders final attribution and publication with `workspaceMu` and `recordMu`. `knowledgeToolOwner` and `workerConfigurationOwner` check the trusted invocation, owning turn, current thread membership and read-only mode. A captured revision confers no authority. |
| `Revision`, `CompetingWrite`, `Commit` | `Store.WritePage`, `Store.DeletePage` and `Store.AppendSchema` compare the expected revision with the occupied file. The per-store mutex serializes application writers. `matchRevision` reads the actual bytes and `publishFile` stages complete replacement bytes before linking or renaming. An append retains existing schema definitions. |
| `MoveSource`, source admission in `Finish` | `resolveKnowledgeSources` calls `ReadProjectHistoryIdentities` and publishes the host's record identity with the page. The history projection checks current project membership and the validity of the resolved row. |
| `AdvanceHistory`, `EditSource`, `ReadSource` | `enrichKnowledgeSources` compares the original stamp with the live row resolved by its locator. Rewind can reuse the locator for a different original turn. A mutable human message can change to distinct bytes and a different content digest with the same locator, row, turn and timestamp. As long as the current identity differs, the old source is unavailable and the wiki page retains its reference. |

At the modeled commit boundary the state transition observes current
ownership, source identity and revision before altering the library.
`receipt.before` retains the exact abstract file and schema state before a
refusal. Read availability is checked against the snapshot for that read;
later source moves can make a subsequent response differ.

## Properties and bounds

The current case checks that publication requires the current writable owner
and matching revision, a refusal preserves the library, admitted evidence
belongs to the current project, an available citation resolves to its
original identity, and schema appends preserve prior definitions. Fault cases
independently remove each guard or inject partial publication. The
in-place-edit fault retains the locator and original metadata while omitting
the revision of its content. Every fault must break its named invariant.

Reachability cases find a cited page write, an available source read, stale
and read-only refusals, preserved but unavailable references after sequence
reuse and in-place edit, and a successful schema append. The runner uses an
expected failure of a negated witness to produce each trace. These are
existential paths. No fairness or eventual-settlement property is assumed.

The state space has one modeled library and page, one starter and one possible
additional schema definition, one pending request and a competing writer, and
a single history locator. A writer or source can independently leave the
modeled project. `MaxRevision = 3` provides unique abstract nonzero revisions
within the exploration. The source can move to another project, be edited in
place, be removed by rewind, and be replaced at that locator. The edit
transition represents distinct bytes with a distinct digest. Same-content
rewrites and restoring the earlier bytes fall outside this bounded history.
The model assumes the filesystem primitive makes a complete file visible in one step
and that conflicting application calls obey the implementation's lock
boundary. Operating system crash durability, arbitrary external editor
interleavings, full Markdown/schema interpretation, SQLite row predicates,
graph traversal and UI rendering require implementation-level evidence.

The corresponding real adapter tests include
`TestKnowledgeDistillationReadsOriginalHistoryWritesAndReadsBacklinksThroughRegistry`,
`TestKnowledgeToolsFollowPlanningAndNuwaAtManagerAndWorkerHTTP` and
`TestKnowledgeAgentRevisionRenameAndDeleteKeepTheOldSourcesUntilExplicitChange`
in `internal/engine/knowledge_http_test.go`. The store races and malformed-file
regressions live in `internal/knowledge`. The model checks the protocol within
the stated bounds, while these tests check the Go and HTTP wiring.

## Manual distillation checkpoints

`DistillationProgress.tla` models two successive batches in a frozen scope.
`Commit` represents acceptance of a batch and the atomic SQLite receipt and
cursor update in `CheckpointDistillation`. An interruption preserves that
cursor. The injected faults advance without acceptance, complete after the
first batch or reset the cursor on resume. The reachability case completes
both batches across an interruption. The model treats acceptance as one
step. The engine and backup tests check the persisted queue and the uncertain
interval between file publication and the SQLite checkpoint.

## Wiki build modes

`WikiBuild.tla` models two source positions, two possible pages, and one run
whose review choice stays fixed across pause and resume. `CheckpointLocal`
represents `scanWikiLocalWindow` committing a receipt after publishing files
or finding no local match. `BeginReview` and `ReviewDecision` represent the
opt-in gate and the verified model boundary in `runWikiBuild`. A local-only
run reaches `BeginDocuments` directly after scanning. `BeginDocuments` and
`Finish` correspond to freezing the document count and recording completion
in `renderWikiDocumentBatch`.

The model checks document coverage and that a local build never enters
review or records a semantic decision. Fault cases demonstrate early local
document publication and unrequested review. Reachability witnesses finish
both modes, without any fairness assumption. The model abstracts whole
records and pages. The loopback and store tests exercise the real HTTP model
boundary, SQLite receipts, resume of older mode-less receipts, and files
published before a failed checkpoint.
