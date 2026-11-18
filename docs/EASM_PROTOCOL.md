# EASM 数据结构与字段说明

本说明定义 EASM 页签需要的数据：资产关系图、资产热力趋势、供应链组件表，以及点击节点后的详情卡片。示例为虚构数据：

- [easm-result-full.json](examples/easm-result-full.json)：完整示例。

## 数据来源

EASM 页签只读一份固定的本地结果，不经过接口、事件或数据库：

- 文件是 `frontend/public/easm-data.json`，构建后按 `/easm-data.json` 提供。
- 文件不入库：真实企业数据不提交到仓库，`.gitignore` 已忽略该路径。
- 没有文件、请求失败或内容不符合本说明时，页签不显示。
- 三个视图的数据放在**同一个 JSON** 里，整体替换，不与旧数据合并。

## 1. 外层结构

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `schema_version` | integer | 必填 | 固定为 `1` |
| `title` | string | 必填 | 右侧页签上显示的文字，通常填 `EASM` |
| `nodes` | array | 必填 | 关系图节点，见第 2 节；没有时填 `[]` |
| `edges` | array | 必填 | 节点间连线，见第 3 节；没有时填 `[]` |
| `summary` | object | 可选 | 顶部统计数字，见第 5 节 |
| `heat` | array | 必填 | 热力趋势，见第 6 节；没有时填 `[]` |
| `components` | array | 必填 | 供应链组件，见第 7 节；没有时填 `[]` |

页签内容区和大图的标题显示根节点的 `label`。

没有数据时写成：`{"schema_version": 1, "title": "EASM", "nodes": [], "edges": [], "heat": [], "components": []}`。

## 2. 节点 `nodes[]`

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 必填 | 非空，在 `nodes` 内唯一；连线和跳转用它引用节点 |
| `kind` | string | 必填 | 节点类型，见下表 |
| `featureKind` | string | `feature` 节点必填 | 特征类型，见下文；其他节点不能填 |
| `label` | string | 必填 | 非空，节点名称；卡片中显示，图上只在鼠标悬停时显示 |
| `ip_count` | number | 可选 | 该节点关联的资产 IP 数，非负。决定节点大小，数值越大节点越大；不填时为最小尺寸 |
| `detail` | object | 可选 | 点击节点时卡片显示的内容，见第 4 节 |

| `kind` | 含义 | 要求 |
| --- | --- | --- |
| `root` | 根节点，即本次梳理的主体 | 有节点时**必须且只能有一个** |
| `company` | 企业 | |
| `org` | 组织 | |
| `domain` | 域名 | |
| `feature` | 特征（证书、MX 记录等），把多个资产关联到一起 | 需要 `featureKind` |

`featureKind` 取值：`org`（证书组织）、`cert`（证书）、`mx`（MX 记录）、`asn`（ASN）、`icp`（ICP 备案）、`icon_hash`（网站图标）、`ipc`（IP 段）、`fid`（FID）。

注意区分：`kind: "org"` 是组织节点；`kind: "feature", featureKind: "org"` 是证书组织特征节点。

**各类特征的 `label` 填什么：**

| 特征 | `label` |
| --- | --- |
| 证书 `cert` | 证书名称（主体 CN）；序列号放在 `detail.serial` |
| ICP `icp` | 备案号 |
| 其他 | 特征值，如 MX 主机名、ASN 号 |

## 3. 连线 `edges[]`

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `source` | string | 必填 | 起点节点的 `id` |
| `target` | string | 必填 | 终点节点的 `id` |
| `confirmed` | boolean | 可选 | `true` 画实线（已确认）；不填或 `false` 画虚线（待确认） |

连线只来自 `edges`。特征节点卡片里的“具有该特征的资产”列表，就是与该特征节点相连的非特征节点，不需要另外提供。

## 4. 节点详情 `detail`

所有节点使用同一个 `detail` 对象，全部字段可选。卡片按节点的 `kind` 和 `featureKind` 只显示适用的字段，不适用的字段即使填了也不显示。

| 字段 | 类型 | 适用节点 | 卡片显示为 |
| --- | --- | --- | --- |
| `description` | string | root、company、org | 简介 |
| `logo` | string | root、company、org | 标识图片地址；页面能直接加载的 `data:` 或 `https` 地址 |
| `website` | string | root | 官网，`http` 或 `https` 地址 |
| `region` | string | root | 国家 / 地区，如“中国 / 北京市” |
| `industry` | string[] | root | 行业列表 |
| `organizations` | array | root | 组织列表，见下表 |
| `reason` | string | company、org、domain | 确认依据：为什么判定属于主体 |
| `features` | array | root、company、org、domain | 有效特征，按类型分组显示，见下表 |
| `serial` | string | cert | 序列号 |
| `issuer` | string | cert | 签发机构 |
| `not_before` | string | cert | 有效期开始，`YYYY-MM-DD` 或 RFC 3339 |
| `not_after` | string | cert | 有效期结束，格式同上；两者都填才显示有效期 |
| `names` | string[] | cert | 覆盖名称 |
| `subject_name` | string | 证书以外的特征 | 备案主体（主要用于 ICP） |

根节点卡片还会显示第 5 节的线索数、FOFA 资产数、FOFA 资产 IP 数。

`organizations[]`：

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 必填 | 组织名称。与某个 `kind: "org"` 节点的 `label` 相同时，可点击跳转到该节点 |
| `count` | number | 可选 | 非负，显示在名称旁 |

`features[]`：

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `kind` | string | 必填 | `featureKind` 的取值之一，或 `domain`（域名） |
| `value` | string | 必填 | 特征值，显示在列表中 |
| `node_id` | string | 可选 | 对应图中节点的 `id`，填了可点击跳转到该节点；必须是存在的节点 |

## 5. 统计数字 `summary`（可选）

| 字段 | 类型 | 显示为 |
| --- | --- | --- |
| `clues` | number | “N 个线索” |
| `fofa_assets` | number | “N 个 FOFA 资产” |
| `fofa_ips` | number | “N 个资产 IP” |

均为非负数。按来源口径填写，不一定等于图中节点数。未知的项不要填；真实零值填 `0`。不提供 `summary` 时顶部不显示统计数字。

## 6. 热力趋势 `heat[]`

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `date` | string | 必填 | `YYYY-MM-DD`，作为横轴标签 |
| `value` | number | 必填 | 该日的热力数值 |

日期必须唯一且升序，前端按数组顺序从左到右画。间隔可以是天或周，保持一致即可。真实零值填 `0`；缺少数据的日期不放这个点，不要用 `0` 代替。

`value` 只决定曲线高低，图上不显示具体数值：没有纵轴刻度，鼠标悬停只显示日期。

## 7. 供应链组件 `components[]`

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 必填 | 非空组件名称 |
| `display_name` | string | 可选 | 与 `name` 不同时，表中显示此名称 |
| `count` | number | 必填 | 非负，表中“聚合计数” |
| `tags` | array | 可选 | 关注标签，见下表；不填时标签列显示“—” |
| `hot_days` | integer | 可选 | 近 30 天热搜上榜天数，点击组件时显示。只在有 `hot` 标签时显示；不填时按 `hot` 标签证据中不同日期的个数计算 |

表格自行排序：先按标签（热搜 > KEV > CVE，无标签最后），再按 `count` 从大到小，再按名称。数组顺序不影响显示。各组件的 `count` 可能有重叠，不能相加得出资产总数。

`tags[]`：

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `type` | string | 必填 | `hot`（热搜）、`kev`（KEV）或 `cve`（CVE）；同一组件内每种最多一个 |
| `summary` | string | 可选 | 点击组件后显示的说明 |
| `evidence` | array | 可选 | 证据列表，见下表 |

标签按提供的内容原样显示，前端不再筛选证据或隐藏标签。是否显示某个标签、放哪些证据，由数据提供方决定。

`evidence[]`：

| 字段 | 类型 | 必要性 | 说明 |
| --- | --- | --- | --- |
| `title` | string | 必填 | 证据标题，如漏洞编号和名称 |
| `date` | string | 可选 | 日期，显示在标题后 |
| `url` | string | 可选 | 必须是 `https` 地址，标题可点击打开 |
| `detail` | string | 可选 | 补充说明 |
