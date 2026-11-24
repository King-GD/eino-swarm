const en = {
  closeInstance: "Close panel instance",
  closeFailed: "Could not close this panel. Try again.",
  invalidEasm: "The saved EASM graph is invalid.",
  regenerateEasm: "Generate a new EASM portrait in a new conversation.",
  resyncFailed: "The plugin could not recover from an update failure.",
  tab: "Task panels", incompatible: "The saved panel definition is incompatible with this plugin build.", handshakeTimeout: "The plugin did not become ready in time.", updateTimeout: "The plugin did not apply its update in time.", loading: "Loading panel…", retry: "Retry", failed: "Panel could not be loaded", disconnected: "Reconnecting — showing saved data", choose: "Choose a panel", settings: "Task panel plugins", enabled: "Enable task panels", client: "FOFA executable", max_assets: "Maximum collected assets", batch_size: "Collection batch size", concurrency: "Concurrent HTTP checks", timeout_seconds: "HTTP timeout (seconds)", flush_interval_ms: "Update interval (milliseconds)", max_state_bytes: "Maximum state bytes", max_update_bytes: "Maximum update bytes", save: "Save panel settings", saved: "Panel settings saved", hint: "Repository plugins render in isolated frames. FOFA collection and HTTP checks run locally; credentials stay outside the panel.",
}
const zh: typeof en = {
  closeInstance: "关闭面板实例",
  closeFailed: "无法关闭此面板，请重试。",
  invalidEasm: "已保存的 EASM 图谱包含无效数据。",
  regenerateEasm: "请在新会话中重新生成 EASM 图谱。",
  resyncFailed: "插件未能从更新失败中恢复。",
  tab: "任务面板", incompatible: "已保存面板定义与当前插件版本不兼容。", handshakeTimeout: "插件未在限定时间内就绪。", updateTimeout: "插件未在限定时间内应用更新。", loading: "正在加载面板…", retry: "重试", failed: "面板加载失败", disconnected: "正在重连，显示已保存数据", choose: "选择面板", settings: "任务面板插件", enabled: "启用任务面板", client: "FOFA 可执行文件", max_assets: "最大采集资产数", batch_size: "采集批次大小", concurrency: "HTTP 验活并发数", timeout_seconds: "HTTP 超时（秒）", flush_interval_ms: "更新间隔（毫秒）", max_state_bytes: "状态字节上限", max_update_bytes: "更新字节上限", save: "保存面板设置", saved: "面板设置已保存", hint: "仓库插件在独立页面中绘制。FOFA 采集与 HTTP 验活在本地运行，凭据不会进入面板。",
}
export function panelText(locale: string, key: keyof typeof en): string { return (locale.startsWith("zh") ? zh : en)[key] }
