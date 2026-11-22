const en = {
  invalidEasm: "The saved EASM graph is invalid.",
  regenerateEasm: "Generate a new EASM portrait in a new conversation.",
  easmSettings: "EASM portrait lookups", easmHint: "Each lookup role calls one MCP tool. Leave the server empty to use the one enabled server that offers each tool; clear a tool name to skip that lookup.",
  easmServer: "EASM MCP server", easmMaxDomains: "Certificate lookups per run (domains)", easmMaxComponents: "Component buckets per aggregation",
  easm_organizations: "Organization lookup tool", easm_relations: "Investment relation tool", easm_branches: "Branch lookup tool", easm_filings: "ICP filing tool", easm_asset_rules: "Asset rule tool",
  easm_certificate_search: "Certificate search tool", easm_asset_lookup: "TLS asset lookup tool", easm_stats: "Aggregation tool", easm_heat: "Attention trend tool", easm_product_rules: "Product rule tool",
  resyncFailed: "The plugin could not recover from an update failure.",
  tab: "Task panels", incompatible: "The saved panel definition is incompatible with this plugin build.", handshakeTimeout: "The plugin did not become ready in time.", updateTimeout: "The plugin did not apply its update in time.", loading: "Loading panel…", retry: "Retry", failed: "Panel could not be loaded", disconnected: "Reconnecting — showing saved data", choose: "Choose a panel", settings: "Task panel plugins", enabled: "Enable task panels", client: "FOFA executable", max_assets: "Maximum collected assets", batch_size: "Collection batch size", concurrency: "Concurrent HTTP checks", timeout_seconds: "HTTP timeout (seconds)", flush_interval_ms: "Update interval (milliseconds)", max_state_bytes: "Maximum state bytes", max_update_bytes: "Maximum update bytes", save: "Save panel settings", saved: "Panel settings saved", hint: "Repository plugins render in isolated frames. FOFA collection and HTTP checks run locally; credentials stay outside the panel.",
}
const zh: typeof en = {
  invalidEasm: "已保存的 EASM 图谱包含无效数据。",
  regenerateEasm: "请在新会话中重新生成 EASM 图谱。",
  easmSettings: "EASM 画像查询", easmHint: "每个查询角色调用一个 MCP 工具。服务器留空时，使用唯一提供该工具的已启用服务器；清空工具名即跳过该项查询。",
  easmServer: "EASM MCP 服务器", easmMaxDomains: "每次证书查询的域名数", easmMaxComponents: "每次聚合的组件数",
  easm_organizations: "组织查询工具", easm_relations: "投资关系工具", easm_branches: "分支机构工具", easm_filings: "ICP 备案工具", easm_asset_rules: "资产规则工具",
  easm_certificate_search: "证书搜索工具", easm_asset_lookup: "TLS 资产查询工具", easm_stats: "聚合统计工具", easm_heat: "关注趋势工具", easm_product_rules: "产品规则工具",
  resyncFailed: "插件未能从更新失败中恢复。",
  tab: "任务面板", incompatible: "已保存面板定义与当前插件版本不兼容。", handshakeTimeout: "插件未在限定时间内就绪。", updateTimeout: "插件未在限定时间内应用更新。", loading: "正在加载面板…", retry: "重试", failed: "面板加载失败", disconnected: "正在重连，显示已保存数据", choose: "选择面板", settings: "任务面板插件", enabled: "启用任务面板", client: "FOFA 可执行文件", max_assets: "最大采集资产数", batch_size: "采集批次大小", concurrency: "HTTP 验活并发数", timeout_seconds: "HTTP 超时（秒）", flush_interval_ms: "更新间隔（毫秒）", max_state_bytes: "状态字节上限", max_update_bytes: "更新字节上限", save: "保存面板设置", saved: "面板设置已保存", hint: "仓库插件在独立页面中绘制。FOFA 采集与 HTTP 验活在本地运行，凭据不会进入面板。",
}
export function panelText(locale: string, key: keyof typeof en): string { return (locale.startsWith("zh") ? zh : en)[key] }
