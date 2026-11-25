# Memory experiment

This is an opt-in measurement tool, not a production feature flag. Read
[the frozen protocol](PROTOCOL.md) before interpreting results. The initial
[controls](controls.json) are synthetic mechanism checks; they do not establish
that memory improves real task completion.

The article's four layers already have counterparts in this repository:

| Article layer | Current implementation | Question tested here |
|---|---|---|
| Transcripts | Durable project events and history tools | Can original evidence correct a later note? |
| Memory notes | Bounded project notes and post-turn review | Do useful preferences survive real capacity limits? |
| Representations | On-demand skills and their descriptions | Can a complete-catalog search improve discovery? |
| Injected context | Turn-start notes and bounded skill index | Can the small prefix remain accurate and useful? |

Dream is a maintenance loop over these layers, not a fifth storage tier. The
candidate deliberately starts with evidence-linked notes only. No wiki, vector
database, autonomous schedule or new live-memory permission is introduced.

## Prepare

Build outside the checkout, using explicit private artifact locations:

```bash
go build -o "$EVIDENCE/zwai" ./cmd/zwai
go build -o "$EVIDENCE/memory-experiment" ./tools/memory_experiment
go test -race -cover ./internal/memoryexperiment ./tools/memory_experiment
python3 -B -m unittest discover -s tools -p 'test_memory_experiment_study.py'
```

The same engine executable runs all four arms. It may prepare its identified
checkout frontend on first startup; finish this headless preparation before
freezing an external run. Do not run the desktop or use an existing data directory.

## Freeze and run

```bash
python3 -B tools/memory_experiment_study.py \
  --binary "$EVIDENCE/zwai" --helper "$EVIDENCE/memory-experiment" \
  --fixture evals/memory/controls.json --out "$EVIDENCE/new-dry-run" --dry-run

python3 -B tools/memory_experiment_study.py \
  --binary "$EVIDENCE/zwai" --helper "$EVIDENCE/memory-experiment" \
  --fixture evals/memory/controls.json --out "$EVIDENCE/new-offline-run" --mock

python3 -B tools/memory_experiment_study.py \
  --binary "$EVIDENCE/zwai" --helper "$EVIDENCE/memory-experiment" \
  --fixture "$FROZEN_FIXTURE" --out "$EVIDENCE/new-measurement" \
  --config "$PRIVATE_PROVIDER_CONFIG" --provider "$PROVIDER_ID" --repeats 2 --seed 0
```

The last command is a real-model measurement and may spend provider quota. It
reads only the selected route from configuration. Every output directory must
be new. A separate engine/data/workspace/memory belongs to each attempt; no
user engine or native window is opened.

The helper's `serve --memory-dir` is an MCP stdio service exposing only
`recall_memory`; its complete metadata results name the existing `skill_view`
reader. The helper's `dream` command receives frozen notes, original source
locators and the original skill catalog, makes one tool-free generation, and
validates the candidate in a private store. Original source memory is never
rewritten. Invalid or unsupported proposals remain failures with raw input,
response and usage retained in private evidence.

Inspect `manifest.json`, each `runs/.../result.json`, actual tool calls and
`traces-post-shutdown.json`. `summary.json` is descriptive and never grants
automatic adoption. A successful process exit or retrieval hit does not prove
that the task was completed correctly. Compare complete costs including Dream,
post-turn review and other durable auxiliary calls; missing usage stays unknown.

A failed process inventory or surviving owned descendant retains private data
and aborts further attempts. Read the cleanup receipt before removing anything
retained. The workspace remains an anchor with full agent access, not confinement.
Keep independent scorers outside it and audit actual accesses; the harness is
not an adversarial evaluation isolation system.
