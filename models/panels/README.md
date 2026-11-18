# Task-panel protocol checks

`make model-check` runs these TLA+ specifications with the pinned TLC version.
Every current case checks its complete safety list. Fault switches restore a
specific suspect behavior; their cases must violate the named invariant.
`Never*` cases must fail to establish that the relevant path is reachable.
`python3 -m unittest tools/test_model_check.py` checks that registration cannot
silently lose a guard, invariant or witness.

| Specification | Finite scope | Code boundary and checked rule |
| --- | --- | --- |
| `TaskPanel.tla` | One panel, two operation IDs, two digests, one captured request competing with another writer, two browser windows | `PublishTaskPanel`: accepted retries preserve identity, changed bodies conflict, new writes compare their captured base, commit precedes delivery. Its revision counts accepted data publications, abstracting lifecycle-only increments. The connection branch re-finds the observed extra-SSE/two-window draft starvation. |
| `PanelOwnership.tla` | Two publisher identities; one live source; one malformed and one healthy recovery thread | `PublishTaskPanel` refuses a different writer while a source is running. `RecoverTaskPanelSourcesForThread` and `Engine.Restore` isolate failure to its owning conversation. |
| `PanelProjection.tla` | Two panel identities, up to four publications/deletion, one outstanding snapshot and one selection change | `frontend/src/store/task-panels.ts`: per-panel revisions and deletion cursors merge an older GET across newer SSE updates; a new panel must not disappear because another panel advanced; authoritative absence settles a covered observation rather than causing endless GETs; an old visit cannot alter the new visit. |
| `PanelFrameDelivery.tla` | One iframe, two desired revisions, one outstanding callback and a bounded automatic resynchronization | `task-panel-frame.tsx` and `sdk.ts`: sent, applied and acknowledged versions have distinct meanings; hidden failed updates retain a snapshot retry; incompatible code does not run; timeout is visible. |
| `PanelProducer.tla` | Two HTTP targets plus equivalent/duplicate input identities, separate observation/submission/reduction/join stages | `fofaworld.Run` and `startWorkersUsing`: deduplication prevents duplicate requests; cancellation cannot drop an already observed response before the reducer; returning requires the owned collector/workers and preceding receipts to join. |
| `MobileTaskPanel.tla` | Two chunks, two transfers/revisions, three visits including A→B→A | `internal/remote/task_panels.go`, `remote-task-panels.ts` and `use-task-panels.ts`: a transfer stays immutable across manifest refresh, complete validated bodies belong to the current visit, old cleanup cannot close a successor, and chat seq remains independent. |
| `InspectorRegistry.tla` | Two projects, two conversations and a standalone conversation for render eligibility; two instance names, one captured callback and up to six actions for automatic focus | `AppPanel`, `RightPanel`, `NuwaPanel` and `useRegisteredInspectorAutoOpen`: project and conversation scope, recorded history, available-tab fallback, retained hidden iframe, project-owned connection status, and rejection of stale, superseded, deleted or replayed automatic-open signals. |

`models/lifecycle/EngineLifecycle.tla` also includes a panel-recovery refusal
before runtime occupation under the existing restoration/Stop ordering.
`models/stream/EventStream.tla` checks that named panel frames cannot overwrite
the independent chat SSE cursor.

## Counterexamples reproduced on the implementation

The source lease and recovery failures are reproduced by
`internal/store/task_panel_ownership_audit_test.go` and
`internal/engine/task_panel_restore_audit_test.go`. The two frontend ordering
failures are reproduced by `frontend/src/store/task-panel-ordering-audit.test.ts`
and `frontend/src/components/app/task-panel-ordering-audit.test.tsx`.
`result_receipt_order_test.go` gates an actual loopback HTTP probe after the
client observes a 403 and cancels before result delivery. The original worker
select loses that receipt; the fixed terminal send preserves it.

The ordinary package tests also exercise real SQLite transactions, failed
writes, competing revisions, rewind and the model/HTTP tool boundary. These
tests are necessary: a TLC action standing for a transaction does not prove
that the Go implementation or its SQLite adapter is atomic.

Mobile's mixed-revision and A→B→A guard mutations run against isolated copies
of the real TypeScript reader/hook. The unchanged control passes; removing each
guard makes its corresponding assertion fail. These calibrate the finite fault
classes without claiming that the final guarded implementation contains those
defects. Real sealed-RPC tests cover immutable large bodies, patch fallback,
scope/token binding and cleanup. Browser tests separately cover the shipped map
with phone touch/rotation and version/error presentation.

The inspector model re-finds the observed unavailable-tab fallback defect and
the cross-project status defect. `nuwa-panel-ownership.test.tsx` uses the real
Nuwa panel and store and observes the first DOM commit for a new project before
its passive subscription reconnects. It failed with the prior project's alert
and Live status still present and passes with project-owned display metadata.
`nuwa-project-mode.test.tsx` exercises the missing-tab fallback on the actual
right panel; `inspector-panels.test.tsx` checks automatic-open guards. The render
branch enumerates one committed view from independent inputs; the focus branch
models store changes between signal capture and delivery, not React scheduling.
An isolated TypeScript control passes all three test files; removing the
metadata ownership, available-tab fallback and instance presence guards causes
the three corresponding assertions to fail.

## Limits

These are finite safety and reachability checks, not an unbounded proof or a
wall-clock termination guarantee. They assume the modeled atomic database and
channel steps and do not establish a remote service's behavior, an OS child's
exit deadline, a browser's scheduling or native WebView presentation.

Producer `applied` means the in-memory reducer accepted an observation. It does
not mean the final observation reached SQLite: Stop cancels publication too,
and a final flush can be refused. The durable source interruption marker then
describes incomplete work without inventing per-asset observations. The
complete rewind reconstruction and JSON Patch value space remain covered by
implementation regressions rather than these finite models. The projection
model does not reopen a deleted ID; implementation tests separately cover an
older GET crossing deletion and same-ID reopening. No liveness
property or native acceptance is claimed by these cases.
