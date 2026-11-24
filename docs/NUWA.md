# Nuwa project collaboration

Nuwa separates planning from execution: the conversation manager plans,
delegates, coordinates and accepts results from worker evidence. Workers perform
the required operations. Agents choose their division of work and configured
models while keeping shared tasks, reported facts and results inspectable.
Enable **Nuwa collaboration** when creating or editing a project, then put the objective,
constraints and acceptance criteria in **Instruction**. Existing projects default
to ordinary mode. No domain workflow or predefined executor role list is required.

Nuwa workers receive **Instruction** directly as shared project context, even
with project memory disabled. They do not have to rely on the planner's task
summary to learn the project's constraints and acceptance criteria. Requirements
addressed to the coordinator do not change an executor's assigned role or tools;
the manager's system prompt and install-wide personality remain separate.
This supplies the original requirements, not proof that an agent followed them.
Nonempty saved worker instructions restore as recorded. Legacy empty instructions
retain the existing fallback to the resolved worker configuration. These are
first-restore semantics, not a promise that all later resumed generations freeze
the same project text.

Connections, credentials and tools remain platform resources configured in
Settings. An instruction can explain how to use them; it cannot connect an
absent service, grant permission or supply missing data. The workspace remains
an anchor with the agent's normal access.

Enable the mode, enter your instruction and open a conversation in the project.
This starter prompt leaves worker roles, task division and model choices to the agent.

```text
Achieve [objective] using [available inputs].
Respect [constraints]. The result is acceptable when [observable checks pass].
Plan from the available evidence and delegate any required operations to workers.
Choose worker roles, task scopes and available models to fit the objective.
Accept the result using the workers' evidence against the acceptance criteria.
For shared findings and results, record their sources and artifact paths.
```

On desktop or Web, the **Work records** tab shows the selected conversation's
records, including work reported by its executors. Use **Expand graph** to
inspect work dependencies and details. See [Work records](WORK.md).

## Planning, models and delegation

The conversation model remains the manager. It does not mount workspace
execution tools or MCP tools, and its provider-hosted builtin execution tools
are disabled. Workers retain their configured provider-hosted tools; ordinary
mode retains its existing configuration. Reading or changing workspace files, running
commands and using external execution tools belong to workers; the manager
coordinates their work and checks their reported evidence. Pure discussion that
requires no such operations does not require spawning a worker. This boundary
does not prescribe a domain, role list, workflow or worker model.

Verification is a mandatory, independent step, not optional advice. Before the
manager accepts a deliverable or completes the goal, a separate executor — not
the one that produced the result — derives its own checks from the stated
objective, constraints and
acceptance criteria and exercises the actual result against the hardest inputs
those criteria imply: boundary and extreme values, empty, large, malformed or
adversarial inputs, and every explicitly required rule or case. The producer's
own passing cases are a claim, not verification. The verifier records its
outcome and evidence with `record_work`, so a failure marks dependent work
**Needs review**. The host bounds the cycle. A check uses `spawn_agent` with
`intent` `verify` and `target` set to that record id; the one repair uses
`intent` `fix` and the same target. Each turn allows one verify and one fix
for a shared record and refuses another before a worker starts, including a
same-role steer or resume. The committed count survives restart. Ordinary
implementation omits `intent` and is not counted. The host does not classify
task text, so a check labeled as ordinary work is outside this allowance and
the turn timeout remains the last fuse. Report the unmet criterion instead of
retrying a refused check. This allowance is a contract constant, not a setting.
The discipline stays domain-agnostic: it prescribes how to verify, not what
the task is.

Nuwa managers and workers receive
`list_models`, `read_work` and, outside planning mode, `record_work`. Workers can
delegate again. All descendants share one Registry, concurrency budget, Stop
and restart lifecycle. A worker may wait for, resume or close its own
descendants; lifecycle control outside its subtree refuses. Messages retain
their existing peer/manager semantics.

`list_models` returns configured, ready choices with exact `ref`, provider/model,
label and known context window. Names are resources, not capability ratings.
Copy its opaque ref into `spawn_agent(..., model_ref: ref)` without constructing
or decoding it. This tool token is separate from the UI/CLI provider ChoiceID.
`default_scope: "conversation"` means the catalog's `default` identifies the
model chosen for the owning conversation turn. It does not identify the model
running a worker. When a worker calls `list_models`, `current` separately
reports its ID, role, parent if any, generation and recorded model binding.
Choose a new worker using `models[].ref`, not the current binding's provenance.
When the project has saved an executor, this turn's new roles use that model:
omit `model_ref` or pass its exact catalog ref. Verification and repair use
the same executor. A different ref returns a correctable error before a worker
is created. The turn copies the executor at start, so a later project edit does
not retarget this turn, and same-role steering, resume and recovery keep the
recorded binding. A project whose four model columns are still empty keeps the
previous rule: with multiple ready choices, each new role must supply an exact
`model_ref`, and omission returns a correctable error pointing to `list_models`
before context forking, dispatch or any worker model request. With at most one
ready choice, omission retains default selection. Roles remain globally unique
in the Registry. The schema keeps `model_ref` optional. Ordinary mode ignores
the saved pair and the generic Registry retains its default behavior. Saving
the pair does not force the planner to spawn a worker.

Every Nuwa worker model call also receives that same current assignment as a
separate runtime System message. It describes the binding already in effect
for that generation, independently of the catalog and shared project data.
Resume and cold recovery use the saved resolved binding, even when the default
or catalog changes. A legacy worker without a recorded binding reports
`model_binding_status: "unknown"`, with no inferred model. The assignment
message does not become history or a fork seed. A tool receipt can still keep
the `current` field from its own call; that historical result does not identify
a later invocation. The assignment leaves project instructions, delegation and
ordinary worker prompts as before.

A delegating agent can call `list_worker_tools` to discover the available worker
tool catalog, including the MCP tools mounted for that turn. Those names are
individual remote functions when the catalog fits the direct-load percent, and
the catalog proxies otherwise. Discovery does not mount these
tools on the manager. Workers retain the full optional catalog allowed by their
configuration even though the manager's own tool surface excludes execution.
For a new role, `spawn_agent` accepts an optional `tool_names` array of exact
names. Omit the array to use the available worker tools, or pass `[]` to omit
optional workspace tools for that worker. Worker
lifecycle and shared-work tools remain available as allowed by the current mode.
Put instructions for using those tools in the task. If a dispatch lists an
already mounted shared-work tool in `tool_names`, the refusal explains how to
correct the selection while retaining the task instructions. The project
instruction can leave workspace tool selection to the agent.

Unknown and repeated names, malformed selections and an explicit scope change
for an existing role are refused before dispatch or steering. A descendant can
inherit the selection or choose a subset. The selection controls the tools
mounted on that worker. A selected shell tool retains its normal access.

Agents can inspect existing project-scoped model-call observations through
`query_history`: discover its schema, then query `llm_calls` for recorded
provider/model, agent, tokens and durations. Workers use the history guidance
when the tool is listed in their own tool set. If a task needs history outside
that selection, the worker can report the missing evidence to its delegator.
Recorded calls describe historical observations, not a price or capability rating. An unfinished request has no final usage yet;
stored numeric zeros cannot distinguish unreported usage from an explicit zero.

`fork_context` copies the spawning agent's conversation as historical evidence,
excluding System messages. A child's context therefore comes from its parent.
Closing a subtree records the affected generations and retains
`already_finished` for results that had already ended.

## Work records and mode changes

The planner and executors use the same [work records](WORK.md) surface
available to ordinary conversations. New records are conversation-scoped
until explicitly shared. Nuwa always mounts the work record tools for its
eligible turns, with reads only in planning. Verification and repair targets
must be visible to the turn.

Changing the mode applies to subsequent turns. Current workers keep their
saved mode and concrete model bindings. The manager delegates read-only
exploration before proposing a plan. Accepting it keeps execution on workers.
An actual mode change advances the project revision. The project inspector
retains access to committed work after disabling Nuwa.

## Updates and recovery

Checkpoints and worker-spawn events retain the explicit workspace tool selection.
A resumed or restored worker receives that same selection. A missing selected
tool causes recovery to refuse. Earlier records without a selection retain the
existing recovery behavior. Checkpoints also retain parent, generation, model
binding and configuration tag.
The app's tag refers to the original durable turn. Restore validates its
conversation/project and rebuilds the tool surface from saved collaboration,
planning and Web execution mode. In-memory workers retain their tool
configuration across later manager turns. Arbitrary Go tools and credentials
are not serialized; current resources must still be available. Bad tags or
unavailable bindings refuse before claiming/resuming a worker, with no silent
model fallback. Legacy untagged/unbound records keep their compatibility path.

If a conversation move completes before a new turn claims its Registry, the
turn must use the destination project. An in-flight admission with an older
project snapshot refuses and can be retried from the destination.

Startup materializes legacy turn owners using the
[turn ownership migration](DATA_MODEL.md#turns--one-request-and-everything-done-to-answer-it).
Tagged workers and Nuwa turns still refuse recovery after a recorded project
change, including moving a turn's standalone conversation into a project.
The same check applies before a new manager can adopt a parked worker. A
worker's shared reads also check its original turn against current project
membership. When a saved owner conflicts, goal Continue returns the conflict
before opening a new turn. Existing workers and checkpoints remain available
for Stop or for recovery after moving the conversation back.
Cold recovery also keeps an uncollected result with its original project.
Previously collected or discarded results remain in history and are omitted
from a fresh Registry in the destination project.

Stop, collection, generation identity and uncertain external effects retain
their existing contracts. A shared report is not new human authorization.

## Verification boundaries

The implementation reference is [Sapper-v2 at c91881d](https://github.com/N1nEmAn/sapper-v2/tree/c91881dc1629409bbf278577dfdaa6b8f383b438).
Its generic graph hook supplies the work frontier and compact recent history on
user submission; its dispatch renderers refresh the graph and recall related
prior nodes. Eino adapts automatic shared-state visibility to each model-call
entry. The reference's commander scripts separately rely on board/STATE usage
and process recovery; they do not inject the board into every internal call.
This adaptation preserves Eino's enforced planner/worker tool separation and
does not import domain tools, predefined roles or an assumed model ranking.

See [testing](TESTING.md) and the [paired evaluation protocol](../evals/nuwa/README.md).
Offline and scripted HTTP tests establish their particular tool, storage,
identity, replay and UI boundaries. `--mock` validates the harness/API lifecycle;
it cannot establish real-model task quality, speed, token savings or billing.
Unit tests do not establish native macOS acceptance.

Earlier evaluations that allowed the manager to execute operations do not
validate this separation contract. The retained six-row f3 calibration had
official results A 1/2, B 2/2 and C 1/2, with no worker or DS requests. It remains
historical evidence for that source and policy only. New source-boundary tests,
a new identified ELF and image, and new calibration are required before claims
about the separated mode; the formal hard54 cohort has executed zero rows.
See the [calibration boundary](../evals/nuwa/RESULTS.md#legacy-f3-calibration-2026-11-06).

CyberGym execution was stopped by platform safety review. There is no CyberGym
score or comparative performance result, and that requirement remains incomplete.

Related boundaries: `F-136`, `C-037`, `C-019`, `C-027`. Work records follow `C-055`. See [API](API.md),
[data model](DATA_MODEL.md) and [library](LIBRARY.md) for integration details.

### 恢复已移除的模型提供商

会话的模型选择只影响后续新执行；已有 worker 保留自己的绑定。Nuwa 已保存模型对时，恢复按钮使用输入框当前显示的项目规划模型，未保存模型对时使用会话模型。若错误为
`restore worker configuration: provider: unknown provider`，先在输入框选择可用模型，
再点击错误行的“使用 … 修复恢复配置”。这会原子保存失效配置的显式替代，保留
原始模型历史、worker 身份、项目归属与工具权限；健康绑定保持原值。会话必须
空闲且无后台 Registry。保存本身不执行任务，之后点击继续／重试即可沿原恢复
流程推进；未确认的外部工具效果仍需按原规则核验。恢复配置随原始轮次持久保存，
重启后有效。
