# 项目级 Markdown 知识库

跟踪 [Issue #185](https://github.com/LubyRuffy/eino-swarm/issues/185)。连接器和 MCP OAuth 登录单独跟踪。 [Issue #184](https://github.com/LubyRuffy/eino-swarm/issues/184)

## 归属与存储

项目拥有知识库，多个对话可以持续补充同一组 Markdown 页面。项目配置放在
`projects/<project>/knowledge/schema.yaml`，页面放在
`pages/<type dir>/<page id>.md`。是否存在 schema 文件决定是否启用。第一次启用写入
`entry` 类型与 `related` 关系，项目可以继续自行定义领域结构。

内核解析类型、字段、关系、导航视图、页面与来源。页面的 YAML front matter
保存稳定 id、类型、标题、别名、字段、出向关系、来源和宿主盖章。Markdown 正文
使用 `[[page-id|显示文字]]` 引用页面。id 是不含冒号的可移植 slug，改标题时
保留 id，按 id 连接的页面仍可打开。物理目录按类型组织。导航按 schema 定义的
关系生成，同一页面可出现在不同分支，单条路径再次遇到同一页面时终止展开。
索引由文件派生并存于内存，文件身份或 stat 变化会重读。红链、过期、缺必填关系
和无法读入的页面进入待处理视图。

```yaml
version: 1
types:
  - id: entry
    dir: entries
    description: A durable page about one subject.
    fields:
      - id: status
        kind: string
        required: true
    sections: [Summary]
    freshness_days: 30
relations:
  - id: related
    from: [entry]
    to: [entry]
    inverse: related
views:
  - id: related-entries
    root_type: entry
    follow: [related]
    depth: 2
```

字段种类为 `string`、`text`、`number`、`integer`、`boolean`、`date`、
`url`、`list`（字符串列表）。关系可选 `required`；没有可解析的有效目标时
进入待处理。自定义视图最多 16 层、10,000 个树节点，超过节点预算时返回
截断标记。schema 与单个 Markdown 文件的读取上限为 4 MiB。

## 读写与来源

启用项目在回合绑定时保存知识库工具能力及有预算的 schema 摘要。普通主管和
worker 使用 `read_knowledge`、`record_knowledge`。计划模式只读。Nuwa
规划者可以读，写权限与 `record_work` 一致。读操作包括 schema、搜索、单页和
lint。页面操作包括新建、完整更新、保留稳定 id 的改标题，以及显式删除。更新、
改标题、删除和 schema 增量提交都比较 revision。校验完成后才原子替换文件；
工具的增量 schema 操作可以新增类型、关系和视图。调整已有类型、目录或 schema 定义由人
直接编辑文件。新建对规范化后的 id、标题和别名查重。

来源可以是外部 URI 或项目历史中的 `{kind, thread_id, seq}`。宿主验证记录归属
和身份，写入原始时间与分类。人类消息和工具结果归为原始资料，助手文字归为
未核实分析，外部引用标为外部来源。删除、移出项目、倒带或改写引用的引导消息
导致原引用不可解析时，页面保留原引用并显示不可用。可用性说明记录可追溯，结论是否获得证据支持还需
阅读原文。直接编辑的无盖章定位符按当前历史解析。最后更新的宿主回合链接到
原对话和 Trace。

项目启用时，提示词把有关研究对象的事实引导到知识库，做事方式留在 Memory，
可复用流程留在 Skills。页面正文按需读取。关闭或从未启用的项目保持原有
提示词。项目历史仍从 `query_history` 读取，外部内容可以继续用已连接的
MCP、`web_search` 和 `web_fetch` 取得。

## 入口

右侧面板使用同一组标签键和激活逻辑。Agents、Files、Trace、任务面板归任务，
Memory、Collaboration、Knowledge 归项目。无项目对话的项目组显示共享库，
Knowledge 提示先移动到项目。项目名在窄面板中截断，悬停可以看全名。

Knowledge 提供启用、按类型和关系的视图、筛选、待处理队列、页面返回栈、
宽幅预览和桌面文件管理器入口。单页显示字段、正文、关系、反链、来源和
Trace。对话和页面中的 wikilink 都能打开知识库。选中标签、写入工具返回
或回合结束时重读已有 HTTP 接口。

从 Knowledge 的提炼菜单选择「当前任务」或「当前项目」。如果尚未启用则先
启用；在项目新建带标题的对话，再走现有普通消息流程。请求只标明范围，
工具指引要求回读原文、查重和记录来源。任务选项带原对话 id 与标题。
周期性项目任务可用同样的普通回合按时间窗口提炼或处理 lint 队列。

## 验收边界

内核回归 schema 和页面校验、revision、外部编辑、关联图、反链和 lint。HTTP
回归路由、项目边界、备份和删除。真实 Registry 通过本地模型适配器执行
`query_history → record_knowledge → read_knowledge`，检查证据身份和 Trace。
Playwright 覆盖分组、启用、导航、wikilink 与两个提炼入口。原生桌面窗口的
验收在 ci-macOS 单独进行。
