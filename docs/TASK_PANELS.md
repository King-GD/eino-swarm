# Task panels and the FOFA world-map example

Task panels are bundled HTML/JS applications. Each panel owns its layout and may use D3, Canvas or another bundled library. The host supplies conversation identity, versioned data and appearance; it does not interpret business fields. Desktop and Web use the same implementation. This is the local Task Panel Protocol v1, not a claim of MCP Apps compatibility.

The paired mobile app bundles the same plugin HTML/JS and iframe host. A PC conversation with a panel gains a Task panels button and opens the first available panel once. The phone uses a full-screen sheet with touch interaction and multiple-panel selection; closing it preserves the frame and zoom without stopping the PC task. A replacement connection to the same PC/thread/database generation retains the last validated projection data while fencing old RPC replies; the source is not restarted. A conversation without a panel has no panel entry. PC selection, conversation selection and database-generation changes invalidate old reads. A phone-only direct-model chat does not run the PC's registered sources.

## Using the example

Install/configure a compatible local `fofa` CLI using its own credential mechanism, then set **Settings → Tools → Task panels**. Provide the query and request geographic collection and HTTP(S) checks in the conversation. The agent can open `fofa-world-map`, then invoke its registered source with `{ "query": "<task-supplied query>", "max_assets": 1000 }`. There is no default query and opening the panel alone performs no collection. Nuwa planners can open/read/publish panels; execution remains on workers. Planning mode exposes only reads.

The inspector opens when it first discovers a new panel instance in the current conversation. Updates to known instances and reconnect replays do not switch the selected tab. Switching tabs keeps the iframe and map zoom. Local map filters, zoom and tooltips do not start tools. Closing the inspector does not stop the task; Stop cancels the task-owned collector and HTTP requests. The current implementation bundles plugins with the app rather than accepting external ZIPs or generated HTML at runtime.

The example uses the local client's `dump --format=json` batch stream, then probes discovered Web endpoints while later batches are still being collected. The adapter supports the installed LubyRuffy/gofofa v0.2.22 behavior, including string-valued NDJSON fields and exit-zero collection errors. Client identity/capability should be revalidated after changing executable. The client path is configurable; credentials are neither passed in argv nor sent to the renderer.

## Map semantics

Country shading counts this run's collected assets after identity deduplication; it is not a global FOFA aggregate or proof of enterprise ownership. Identity includes the recorded IP:port, protocol and logical origin; equivalent omitted/explicit HTTP80 or HTTPS443 ports identify one asset and one probe. The palette has a logarithmic scale and labeled legend. Starting/completed checks update geographic markers. Colocated assets are grouped without invented jitter. Missing/invalid coordinates remain in counters and are listed as unlocated; country centroids are never used as asset locations. FOFA coordinates are reported geolocation rather than verified physical positions. Bundled Natural Earth boundary source/version is recorded in `frontend/src/task-panels/fofa-world-map/SOURCE.md`.

HTTP checks connect directly to the recorded IP:port, preserving the logical Host/SNI. They issue one GET, do not follow redirects, do not retry, ignore environment proxies, validate TLS normally and retain no response body. Any valid HTTP response, including 403/404, is responsive. Timeout, TLS, connection and cancellation are separate observations; no response does not prove that an asset is offline. Non-Web records are skipped. The source performs no additional-port discovery.

During collection the display reports discovered and processed counts. A fixed completion percentage is available after collection ends and applies only to imported eligible targets. Collection completeness is separate: a process exiting is not proof that all FOFA data was obtained. Quota/error/output uncertainty retains partial or unknown status. The import cap is enforced against raw rows, including duplicates; discovered counts are deduplicated.

## 开发者接入

### 宿主注册表与插件目录

桌面和 Web 的右侧栏通过[宿主注册表](../frontend/src/components/app/inspector-panels.tsx)判断入口是否可用，并生成标签和内容。`scope` 决定从哪个项目或会话读取数据，`activation` 决定入口何时出现，`renderer` 决定用哪个组件渲染。

| 注册项 | `scope` | `activation` | `renderer` |
| --- | --- | --- | --- |
| `nuwa` | `project` | `project_mode`：项目启用女娲或已有历史；不自动切换标签 | `react`：选中时挂载女娲协作组件，使用现有项目数据流 |
| `task-panels` | `thread` | `agent_open`：当前会话有实例才显示入口，首次发现新实例时自动打开 | `iframe`：同一标签内可切换多个实例，其他标签选中时保留 iframe |

解析注册项时会绑定具体的项目或会话 ID。当前标签因归属变化或实例移除而不可用时，右侧栏暂时显示 Agents，不覆盖原选项。女娲从会话所属项目读取；只在没有活动会话时使用侧栏所选项目。任务面板实例归会话，采集执行归启动它的任务，两者有各自的生命周期。

按下文编写 HTML/JS 插件后，把定义加到 [catalog.json](../frontend/src/task-panels/catalog.json) 即可共用 `task-panels` 入口，无须改宿主注册表。新增宿主面板类型时，在宿主注册表中声明归属、激活、渲染和挂载规则，并验证切换作用域时不会带入旧面板。`catalog.json` 是构建和 Agent 共用的插件目录；女娲的宿主注册项不进入该目录，也不出现在 Agent 提示词的可用任务面板列表中，不能用 `open_task_panel` 打开。配对手机继续使用会话级任务面板的现有入口，不展示女娲协作图。

先约定面板状态：记录的稳定 ID、进度和错误字段，以及空状态。`schema` 校验输出状态，`initial_state` 是首次打开时的有效空状态；专用数据源的输入由 `source.input_schema` 另行约定。现有工具已能提供所需结果时，可以只新增前端插件，复用宿主的发布工具。

### 前端开发：布局、交互和渲染

1. 在 `frontend/src/task-panels/<id>/` 放入 `index.html`、`main.ts`、样式和本地资源。插件自己管理 DOM、D3 或 Canvas。保留 HTML 的 `<head>`、`</head>` 和 `</body>`，构建器在这些位置插入 CSP、样式和脚本。
2. 在 [catalog.json](../frontend/src/task-panels/catalog.json) 注册 `id`、`version`、`name`、`description`、`entry: "task-panels/<id>.html"`、`schema` 和 `initial_state`。`description` 写清插件适合哪些任务；引擎把描述提供给 Agent，由 Agent 判断是否调用 `open_task_panel`。仅注册插件不会显示面板。前端和 Go 宿主使用同一份目录；不兼容的代码或状态变更应递增 `version`，历史不兼容实例会拒绝加载。
3. 从 [SDK](../frontend/src/task-panels/sdk.ts) 调用 `createTaskPanel({ onSnapshot, onPatch?, onHostContextChanged?, onDispose? })`。先实现 `onSnapshot`，能从完整状态正确绘制；需要增量渲染时再实现 `onPatch(operations, nextState, context)`，用稳定 ID 更新变化的图元。不实现 `onPatch` 时，SDK 先应用补丁，再交付完整状态。两种方式都不重载 iframe。通过上下文适配尺寸、主题和可见性，在 `onDispose` 清理资源。
4. 同一份插件进入桌面、Web 和配对手机的构建。验证窄屏、触摸、旋转及空状态；在同一实例上连续送入 patch 和 snapshot，检查数据及缩放等交互状态。增加依赖时，验证前端和手机都能独立构建。

The Vite task-panel builder bundles scripts/styles/data into one HTML document. Executable code is repository-owned. Each document runs in an opaque-origin `sandbox="allow-scripts"` iframe with a restrictive CSP: no application DOM/storage, external network, nested frames or workers. The existing workspace HTML-preview policy is separate. Runtime data is delivered only over the dedicated host channel.

宿主上下文包含面板和会话 ID、版本、主题、语言、尺寸、可见性和数据源状态。工具结果始终按数据处理，不作为可执行 HTML 或指令。

第一方面板也可以不用 iframe：catalog 中设置 `renderer: "native"` 且 `entry` 为空，再在 [native-task-panels.tsx](../frontend/src/components/app/native-task-panels.tsx) 按插件 ID 注册 React 组件。桌面、Web 和配对手机共用这份注册表；未注册的 native 定义显示不兼容提示，不会回退到 iframe。组件只读面板 state，文案放进自己的 en/zh 词表，跟随用户语言。

### 后端开发：选择数据入口并发布状态

**复用现有工具结果。** Agent 调用 `open_task_panel({plugin_id})` 拿到 `panel_id`，再用 `read_panel_state({panel_id})` 读取当前版本和 Schema。把已有结构化结果经 `publish_panel_update` 发布：全量用 `snapshot`，局部变化用 JSON Patch。请求带当前 `base_revision` 和唯一 `operation_id`；相同请求重试保留原 ID 和请求体，版本冲突时重新读状态、按新版本生成新操作。此路径不配置 `source`，无需新增 Go producer。

**注册 Go producer。** 需要程序直接持续发布进度时，在 catalog 中声明 `source.id` 和 `source.input_schema`，并在 [panelProducerFor](../internal/engine/task_panel_sources.go) 注册同一 source ID 的适配器（业务代码放在 `internal/panelplugins/<id>`，引擎只保留这一处接线）。Agent 打开面板后调用 `run_panel_source({panel_id,input})`。producer 校验业务输入，遵守任务的 `context`、取消和资源限制；将每批结果折叠为符合 Schema 的完整状态，串行调用 `publish(ctx, state)` 并处理错误。引擎比较状态生成 patch，补丁比完整状态大或操作过多时改用 snapshot，并负责身份、版本、持久化和通知。producer 不直接操作 SQLite、SSE 或 Pairlink；配对手机只读同步同一份已提交状态。

后端验收使用隔离数据，经真实工具入口覆盖首次打开、幂等重试、版本冲突；专用 producer 另外覆盖输入拒绝、运行进度、取消和失败。端到端检查见 [Validation](#validation)。

## Protocol v1

The agent tools are `open_task_panel`, `read_panel_state`, `publish_panel_update` and `run_panel_source`. Open and write receipts carry identity and version rather than bulk state. Complete reads respect the ordinary tool-result budget; excessive data remains accessible in the panel rather than silently clipped into a malformed tool result.

A publication has `protocol_version:1`, `panel_id`, `operation_id`, `base_revision`, and either `kind:"snapshot", state:{...}` or `kind:"patch", operations:[...]`. State is a JSON object validated against the pinned Schema. The runtime supplies thread/turn/agent/generation/tool-call provenance. A transaction compares revision, validates the resulting state and atomically saves state, operation receipt and delivery record. Accepted identical retries return their original receipt, even after later updates; a changed operation body conflicts. Resource limits apply to new mutations. There is no automatic retry of an external source.

A running registered source exclusively owns new writes to its panel using its complete runtime identity. Another publisher receives a conflict until that source finishes or is interrupted; reading a previously accepted identical receipt does not transfer ownership. A claim displaced before the source starts refuses before any external work.

The host transfers one MessageChannel after verifying the iframe window identity. Version/patch capability negotiation precedes data delivery; plugins acknowledge applied revisions. A patch with the wrong base requests a snapshot. Failed callbacks allow a bounded resynchronization; startup/apply timeout surfaces a local error with explicit retry. Hidden panes do not accumulate an unbounded render queue and catch up from current state when shown.

Read endpoints: `GET /api/task-panels/catalog`, `GET /api/threads/:id/panels`, `GET /api/threads/:id/panels/:panel`. Browser clients opt into `GET /api/threads/:id/events?panels=1`: this existing conversation SSE carries named `panel_ready`, `panel_update` and `panel_error` frames alongside chat. Panel frames never set SSE id; their monotonic cursor remains inside the body and cannot overwrite chat Last-Event-ID. Reusing the physical connection leaves HTTP/1 connection capacity for two-window draft saves. Legacy chat clients are unchanged. Explicit API consumers may use `GET /api/threads/:id/panels/events`, starting with `ready {panels,cursor}` from one atomic read. Reconnect repairs from a fresh snapshot without rerunning tools.

### Paired mobile transport

Mobile data uses the existing sealed Pairlink connection, never the PC's Gin HTTP origin. It polls a small `task_panels {thread_id}` manifest containing instance identity, plugin version, revision and source status. A changed visible instance uses `task_panel_read {thread_id,panel_id,base_revision?}`. The PC freezes one JSON body: `{panel}` for a full snapshot, or `{change}` for the latest patch when its exact base and instance incarnation match. A missing base or gap produces a snapshot. Newer manifests do not cancel a transfer already in progress.

The reply's `panel_transfer` contains `{id,thread_id,panel_id,revision,size,sha256,offset,next_offset,data,more}`. Each `data` is base64 of at most 36KiB, within the 64KiB sealed frame. The complete JSON envelope is limited to 64MiB, including its definition. Continuations use `task_panel_read {thread_id,panel_id,transfer_id,offset}`. All chunks retain the same identity, revision, size and SHA-256. The phone checks ordering and the complete hash/JSON/identity before applying data. It never renders a partial body, changes chat seq, loads HTML from the PC, or invokes a producer.

Each connection owns one frozen transfer. A successful new first read replaces it; failed first reads retain the previous valid transfer. `task_panel_cancel {thread_id,panel_id,transfer_id}` releases only that exact transfer; old cancellation cannot remove a successor. Link close releases the cache. Invalid scope/offset, expired tokens and oversized replies fail explicitly; the manifest is never silently clipped. Continuations verify visibility using a narrow metadata query rather than copying the entire live state again.

Manifests continue while the sheet is closed, but after initial discovery hidden panels do not start more bulk reads. Reopening reads the latest committed version. Failed patch application falls back to a complete snapshot; an older PC that returns `unknown_op` simply has no panel entry. The transfer limit is not acceptance of 64MiB per panel across an arbitrary number of mounted mobile plugins; large multi-panel memory and device performance require separate measurement.

`GET/PUT /api/task-panels/settings` reads/saves only this configuration. PUT accepts an edited subset and preserves concurrent unrelated settings. Defaults: enabled, client `fofa` discovered through PATH, 1000 raw rows, batches100, concurrency16, per-target8s, publication100ms, state8MiB, update1MiB. Configuration is stored under `task_panels` in config.yaml. Source queue work is bounded by those budgets.

SQLite stores panel projections, mutations and delivery records. Rewind rebuilds retained state with higher versions; deletion removes all owned records and rejects late writes. Source lifecycle is durable: running, finished or interrupted. Stop/publisher failure attempts a narrow terminal marker for only the exact source identity; unavailable storage produces a WARN rather than a claimed durable marker. Startup marks orphaned running sources interrupted without sending network requests. Restored display is not resumed collection. Full/project backups include panel data, and omitted conversations exclude it.

Startup settles sources separately for each conversation before its ordinary recovery. An unreadable source prevents that conversation from executing and is reported in the aggregated restoration failure; independent conversations still recover. The browser merges snapshot replies by panel revision and deletion cursor, so a newer update to another panel cannot hide a newly opened instance. An authoritative snapshot also settles covered missing instances after a lost deletion notification; older replies cannot resurrect an instance across deletion/reopening. Failed iframe application remains pending across visibility changes until an applied acknowledgement or a visible terminal failure.

Stop preserves an HTTP observation already returned to the source reducer, but does not grant permission to publish new data after cancellation. A failed final flush can leave the display at its last committed state; source metadata marks the incomplete work. A recorded collection failure keeps its known reason rather than being relabeled as user cancellation.

## EASM portrait

`easm-asset-portrait` is a native panel with a registered source. The agent confirms the subject (asking the user when it is ambiguous), opens the panel, does the discovery lookups with its own MCP tools and pushes what it found. The producer never discovers anything itself.

`run_panel_source` takes one batch of that work:

```text
{subject,
 description?, logo?, website?,
 companies?:    [{name, parent?, relation?, share?, registry_id?, confirmed?, source, via?}],
 domains?:      [{domain, parent?, confirmed?, source, via?}],
 certificates?: [{serial, domain, issuer?, subject_cn?, subject_org?, names?, not_before?, not_after?, valid?, match?, expired?, source, via?}],
 query?}
```

`subject` is required, and `source` is required on every company, domain and certificate: it is the tool that reported the entry, stored as that node's evidence. `parent` names the upper company; omitting it hangs the entry under the subject. `query` is the final FOFA asset query; only a batch that carries it starts the totals, attention trend and components. The catalog's `input_schema` bounds every list and every field length, and refuses unknown fields.

Batches are cumulative. The producer restores the graph from the panel state the call started with and merges the new entries into it, so the agent can push findings as they arrive instead of waiting for a complete picture. A call whose `subject` names another organization than the panel's `title` starts a new portrait and discards the previous graph and metrics; one trailing bracketed short name, such as a brand after the registered name, is ignored in that comparison. A company entry naming the subject itself (same legal name, or the registry ID the root already carries) enriches the root instead of becoming a separate node, so children naming the subject as parent stay under the root. Confirmation only ever upgrades: a later batch may confirm an edge another tool doubted, but no batch re-parents a node or takes a confirmation away. An entry whose `parent` is not on the graph hangs under the subject unconfirmed and is counted in the receipt's `unresolved_parents`. Domains are bounded by `max_domains` across the whole portrait; entries over it are refused and counted in `refused_domains`. Two same-name companies with different identities are two registrations, so neither can be the parent an entry names.

Graph rules are enforced by one `Graph` type: a single root without incoming edges; every other node keeps its first source edge; a later observation may enrich details but never re-parents a node or closes a cycle. Domains use one IDNA normalization everywhere, so any tool reaches the same node. Node evidence is structured `detail.basis[] = {source, via?, date?}`; the renderer words it in the user's language and shows an unrecognised `source` (a tool name) exactly as stored.

- Companies take the reporting tool's `registry_id` as their identity when it gives one, otherwise their name. `relation` and `share` are shown on the card as reported.
- Domains hang under their company, or under the subject when none is named or the named one is not on the graph.
- Certificates hang under the domain they were observed on; a domain the agent has not pushed yet enters as a pending clue under the subject. Identity is serial plus issuer, so a shared certificate stays one node and its second domain records it as an effective feature instead of a second edge. A certificate's covered names never create domain nodes: what enters the portrait is the agent's finding. Only a valid, matching, unexpired certificate confirms the domain → certificate edge.

With `query` set, the producer runs `summary`, `heat` and `components` concurrently and publishes each completion, so the panel fills in as the slower aggregations answer. Each calls the tools bound to its roles in `task_panels.easm` ([Config](CONFIG.md#task-panels)). `collection.steps[]` records `pending`, `running`, `done`, `failed`, `unavailable` or `interrupted` with a stable `code` (`tool_error`, `malformed_reply`, `not_configured`, ...); a remote error message is never stored. A failed aggregation does not stop the others or the agent's turn. The producer reaches remote tools only when the turn itself may mount MCP: plan mode, a read-only turn or a restricted account gets `unavailable` steps with no remote call, so a panel source cannot bypass the account's tool policy. A batch without `query` only merges: the collection is `idle` while a step is unfinished and `done` when all three are, and a finished aggregation is not repeated until the query changes. A new query clears the previous range's numbers before the new ones arrive. Stop marks unfinished steps `interrupted`, so a reload never shows a spinner for work that no longer runs.

- `summary` reads the exact asset total and distinct IPs; `clues` counts non-root nodes.
- `heat` requires strictly ascending `YYYY-MM-DD` points.
- `components` aggregates only hardware (`product1`) and applications (`product5`) for the supply chain, excluding the operating-system, service and support layers. FOFA merges the two requested layers into one `product` list. When the product-rule role is configured, the producer resolves numeric rule IDs to names; a failed name lookup keeps the IDs. These fields are selected by the Go producer; changing an agent Skill does not change its MCP requests.

The receipt returned to the agent is counts, step outcomes and what could not be placed (`nodes`, `edges`, `components`, `steps`, `unresolved_parents`, `refused_domains`); bulk data stays in the panel. The call that collected the final aggregations also carries `next`, telling the agent to summarize the panel and end the turn without further tools. When an aggregation failed or was unavailable, `next` names those steps instead of claiming the panel holds every number, and still ends the turn rather than repeating aggregations that just failed. A state that breaks the tree invariants is not continued: the call fails and the panel keeps its last committed revision.

The renderer shows the graph, attention trend and component table and keeps all copy in `frontend/src/lib/easm/messages.ts`. Each section with data always shows it. Without data it shows the same spinner while the host reports the source `running` and its step is pending or running, or while the owning conversation is still running and the step has not finished (a first batch may be followed by one with a query). The host's `source_status` decides whether a producer is collecting now: a stored step that still says `running` after an engine restart or Stop proves nothing on its own. The graph is filled by whole batches, so no step owns it: it waits while a source call is collecting or the owning task can still push another batch. "No data" appears only once nothing more can arrive. The side panel and expanded graph share this loading behavior. Component trending days and KEV/CVE tags have no configured source yet and stay empty.

## Validation

`go test -race ./internal/taskpanel ./internal/store ./internal/panelplugins/fofaworld ./internal/panelplugins/easm ./internal/engine ./internal/server` covers protocol, storage, producer and real model/HTTP boundaries. Producer fixtures include the real installed CLI against a loopback fake FOFA API and local HTTP/HTTPS targets: no real account or public asset is needed. `make model-check` includes task-panel ownership, commit-before-delivery, idempotency, cleanup identity and orphan-recovery counterexamples. EASM producer tests use a fake tool caller per aggregation plus merge fixtures (incremental batches, confirmation, unknown parents, subject reset, domain budget, certificates) and one engine test through the real model → two `run_panel_source` calls → MCP streamable HTTP boundary; every published state is validated against the catalog schema, and an already stored panel of the previous pipeline still validates. `frontend/e2e/easm.spec.ts` drives the native renderer with served panel state and real SSE patches, including a restart whose stored steps still say running while the host reports the source interrupted. Frontend unit tests and the task-panel Playwright case exercise the packaged iframe, incremental map, zoom retention, full snapshots, version refusal and failed-plugin handling. Native desktop acceptance remains a separate ci-macOS gate.

The [model mapping and bounds](../models/panels/README.md) cover competing writers, source ownership, per-conversation recovery isolation, snapshot/SSE crossings, iframe acknowledgements and probe receipt cancellation. These checks prove finite protocol properties under their stated assumptions; they do not replace implementation race tests, native acceptance or establish that every possible failure is absent.

Mobile verification also uses actual sealed `LinkPump`/Store regressions, client chunk/visit/patch tests and `mobile/e2e/task-panels.spec.ts` on Chromium and WebKit phone profiles. An isolated source copy builds with only mobile dependencies, including the shared plugin source. `MobileTaskPanel.tla` models two chunks/two transfers and A→B→A visit fencing; isolated guard mutations verify the real client's revision and visit regressions reject the broken variants. These fixtures use synthetic assets and do not accept an installed iOS/Android package or physical-device behavior.

The native-only fixture is explicitly enabled by `ZWAI_MOCK_TASK_PANELS=1` inside the private ci-macOS case. It uses real owned read/open/publish tool receipts and synthetic CN/US records, with no FOFA or target requests. `tools/native_task_panels.py` reuses the existing runner/preflight, native fill/Send, screenshot and cleanup; its50s scenario and60s round budgets include cleanup. The initial image must be captured before its case-bound gate releases the final patch. This accepts native presentation separately from real CLI/local-probe fixtures.
