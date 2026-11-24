# Unified project distillation

One manual background review reads a bounded snapshot of project history. It
proposes updates to project notes, Skills and Markdown knowledge together.
The Memory and Knowledge tabs share one project receipt and the original turn
Trace. The action supports the latest successful turn, the current
conversation and the current project.

The engine uses the existing per-project review queue. The host looks up the
current project and selects terminal turns from a SQLite read snapshot. It
prioritizes human corrections, then paired tool observations and assistant
text within the shared review budget. Every visible event gets a unique
review-local handle, mapped to its original conversation and event sequence.
Failed turns contribute explicit human requirements for notes. Ordinary
review tools handle automatic memory review and the shared Skills library.

The reviewer can inspect related saved material and submits one complete
proposal. The host verifies scope against the captured event map, rechecks
current identity before writing, and uses the existing notes and knowledge
revision checks. One store can commit before another fails. Receipts carry
actual writes, while interruption of a run with a missing terminal receipt
leaves those writes uncertain. The host persists only the latest run receipt
per project and checks the Trace anchor against current conversation ownership.

`models/knowledge/ProjectDistillation.tla` checks exclusive manual admission,
scope-bound writes and success receipt publication for two requests, one
project, two source memberships and three targets. Fault variants remove each
guard independently. The reachable success witness saves all three targets.
The model assumes scoped source and sink results are provided by the adapters.
The HTTP and Playwright regressions cover those adapters, including
cross-conversation seq reuse, source edits, partial commits and both panels.
