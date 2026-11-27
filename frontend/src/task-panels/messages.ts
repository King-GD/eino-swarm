const en = {
  closeInstance: "Close panel instance",
  closeFailed: "Could not close this panel. Try again.",
  invalidEasm: "The saved EASM graph is invalid.",
  regenerateEasm: "Generate a new EASM portrait in a new conversation.",
  resyncFailed: "The plugin could not recover from an update failure.",
  tab: "Knowledge graph", empty: "No knowledge graph data yet", incompatible: "The saved panel definition is incompatible with this plugin build.", handshakeTimeout: "The plugin did not become ready in time.", updateTimeout: "The plugin did not apply its update in time.", loading: "Loading panel…", retry: "Retry", failed: "Panel could not be loaded", disconnected: "Reconnecting — showing saved data", choose: "Choose a panel", settings: "Task panel plugins", enabled: "Enable task panels", max_state_bytes: "Maximum state bytes", max_update_bytes: "Maximum update bytes", save: "Save panel settings", saved: "Panel settings saved",
}
const zh: typeof en = {
  closeInstance: "关闭面板实例",
  closeFailed: "无法关闭此面板，请重试。",
  invalidEasm: "已保存的 EASM 图谱包含无效数据。",
  regenerateEasm: "请在新会话中重新生成 EASM 图谱。",
  resyncFailed: "插件未能从更新失败中恢复。",
  tab: "知识图谱", empty: "暂无知识图谱数据", incompatible: "已保存面板定义与当前插件版本不兼容。", handshakeTimeout: "插件未在限定时间内就绪。", updateTimeout: "插件未在限定时间内应用更新。", loading: "正在加载面板…", retry: "重试", failed: "面板加载失败", disconnected: "正在重连，显示已保存数据", choose: "选择面板", settings: "任务面板插件", enabled: "启用任务面板", max_state_bytes: "状态字节上限", max_update_bytes: "更新字节上限", save: "保存面板设置", saved: "面板设置已保存",
}
export function panelText(locale: string, key: keyof typeof en): string { return (locale.startsWith("zh") ? zh : en)[key] }
