export const modelSettingsEn = {
  "settings.models.title": "Models",
  "settings.models.desc":
    "Configure conversation, voice input, and read-aloud models. Gateway models come from your account; local mode lets you configure your own services.",
  "settings.models.providers": "Providers",
  "settings.models.providersDesc":
    "Each endpoint is a row. The switch puts it in the composer. Open one to edit the URL, key, and default model.",
  "settings.models.listed": "Show {name} in the composer",
  "settings.models.listedHint":
    "Off hides this endpoint from the model list. A conversation already using it stays on it. The last one stays on.",
  "settings.models.add": "Add a provider",
  "settings.models.provider": "Provider",
  "settings.models.providerPlaceholder": "Name this provider",
  "settings.models.noModel": "No model yet",
  "settings.models.default": "default",
  "settings.models.makeDefault": "Make default",
  "settings.models.remove": "Remove {name}",
  "settings.models.details": "{name} details",
  "settings.models.baseUrl": "Base URL",
  "settings.models.apiMode": "API mode",
  "notice.cloudTool": "Cloud tool: {type} · {status}",
  "settings.models.apiModeHint": "Choose the protocol this endpoint supports. Cloud built-in tools require Responses.",
  "settings.models.builtinTools": "Cloud built-in tools (JSON)",
  "settings.models.builtinToolsHint": "Executed by the provider in Responses, separately from local function tools. Enter a JSON array with a type in each entry; tool options are preserved. Supported: web_search, web_search_preview, file_search, code_interpreter. Empty means disabled. Availability depends on the provider and model.",
  "settings.models.builtinToolsInvalid": "Enter a JSON array of tool objects, each with a non-empty type. Settings will save after this is corrected.",
  "settings.models.builtinToolsNeedsResponses": "Select Responses or clear the cloud tool list before saving. Your tool configuration is retained.",
  "settings.models.apiKey": "API key",
  "settings.models.apiKeySet": "A key is stored. Type to replace it.",
  "settings.models.apiKeyHint": "Leave empty for endpoints that need no key.",
  "settings.models.defaultModel": "Default model",
  "settings.models.defaultModelHint":
    "{n} model{s} on this endpoint. Switch per conversation in the composer.",
  "settings.models.defaultModelEmpty":
    "Discover to list every model this endpoint serves. You only pick the default here.",
  "settings.models.discover": "Discover models",
  "settings.models.discoverFailed": "Couldn't list models",
  "settings.saveFailed": "Couldn't save settings",
  "settings.loadFailed": "Couldn't load settings",
  "settings.models.pickDefault": "Pick a default",
  "settings.models.typeModel": "discover, or type a model name",
  "settings.models.window": "Context window (tokens)",
  "settings.models.windowHint":
    "How many tokens a model on this endpoint can take, until a name has its own window.",
  "settings.models.windowPlaceholder": "from the endpoint, or leave blank",
  "settings.models.windows": "Context windows",
  "settings.models.windowsHint":
    "Each name on this endpoint has its own limit. Discover fills a value when the listing includes one.",
  "settings.models.windowFor": "{name} context window",
  "settings.models.windowFallback": "Fallback for other names",
  "settings.models.windowFallbackHint":
    "Used only when a name has no window of its own. Do not put one model's limit here.",
  "settings.models.timeout": "Request timeout (seconds)",
  "settings.models.timeoutHint":
    "How long a started stream may stay silent. A call that is still streaming is not cut off; a silent endpoint is. Compact uses the same timeout.",
  "settings.models.firstByteTimeout": "First-byte timeout (seconds)",
  "settings.models.firstByteTimeoutHint":
    "How long to wait for the model's response headers. Increase this if the model is slow to start. Separate from the request timeout after streaming starts. 0 uses the default.",

  "settings.aux.title": "Auxiliary models",
  "settings.aux.desc":
    "These jobs use the conversation's model unless you pin one.",
  "settings.aux.titleGen": "Title generation",
  "settings.aux.compact": "Compact summary",
  "settings.aux.badge": "conversations",
  "settings.aux.auto": "Automatic · this conversation's model",
  "settings.aux.automatic": "Automatic",
  "settings.aux.titleAria": "Title generation model",
  "settings.aux.compactAria": "Compact summary model",

  "settings.searchEmbed.title": "Conversation search",
  "settings.searchEmbed.desc":
    "Keyword search is always on. Configuring an embedding model automatically enables semantic search; clear it to disable.",
  "settings.searchEmbed.embedding": "Semantic search",
  "settings.searchEmbed.embeddingHint":
    "Configure a model to let ⌘K rank conversations by meaning as well as words.",
  "settings.searchEmbed.model": "Embedding model",
  "settings.searchEmbed.modelHint":
    "Only models the server classified as embedding.",
  "settings.searchEmbed.modelAria": "Embedding model",
  "settings.searchEmbed.badge": "search",
  "settings.searchEmbed.pick": "Select a model",

} as const

export const modelSettingsZh: Record<keyof typeof modelSettingsEn, string> = {
  "settings.models.title": "模型",
  "settings.models.desc":
    "配置对话、语音输入和语音朗读模型。网关模式由账号提供模型，本地模式可配置自己的服务。",
  "settings.models.providers": "提供商",
  "settings.models.providersDesc":
    "每个接口一行。开关决定它出不出现在输入框的模型列表里。打开后可改 URL、密钥和默认模型。",
  "settings.models.listed": "在输入框里显示 {name}",
  "settings.models.listedHint":
    "关掉后，这个接口不会出现在输入框的模型列表里。已经在用它的会话继续用。最后一个不能关。",
  "settings.models.add": "添加提供商",
  "settings.models.provider": "提供商",
  "settings.models.providerPlaceholder": "给这个提供商起名",
  "settings.models.noModel": "还没有模型",
  "settings.models.default": "默认",
  "settings.models.makeDefault": "设为默认",
  "settings.models.remove": "移除 {name}",
  "settings.models.details": "{name} 详情",
  "settings.models.baseUrl": "接口地址",
  "settings.models.apiMode": "API 模式",
  "notice.cloudTool": "云端工具：{type} · {status}",
  "settings.models.apiModeHint": "选择接口支持的协议。云端内置工具需要使用 Responses。",
  "settings.models.builtinTools": "云端内置工具（JSON）",
  "settings.models.builtinToolsHint": "由提供商通过 Responses 在云端执行，与本地函数工具分开。填写 JSON 数组，每项必须包含 type；工具参数会原样保留。当前支持 web_search、web_search_preview、file_search、code_interpreter。空列表表示禁用，实际可用性取决于提供商和模型。",
  "settings.models.builtinToolsInvalid": "请填写工具对象组成的 JSON 数组，每项包含非空的 type。修正后才会保存设置。",
  "settings.models.builtinToolsNeedsResponses": "请切换到 Responses，或清空云端工具列表后再保存。当前工具配置会保留。",
  "settings.models.apiKey": "API 密钥",
  "settings.models.apiKeySet": "已存有密钥。输入即替换。",
  "settings.models.apiKeyHint": "不需要密钥的接口可以留空。",
  "settings.models.defaultModel": "默认模型",
  "settings.models.defaultModelHint":
    "这个接口上有 {n} 个模型。在输入框里按会话切换。",
  "settings.models.defaultModelEmpty":
    "点发现可列出这个接口上的全部模型。这里只选默认。",
  "settings.models.discover": "发现模型",
  "settings.models.discoverFailed": "没法列出模型",
  "settings.saveFailed": "没法保存设置",
  "settings.loadFailed": "没法加载设置",
  "settings.models.pickDefault": "选一个默认",
  "settings.models.typeModel": "发现，或自己输入模型名",
  "settings.models.window": "上下文窗口（tokens）",
  "settings.models.windowHint":
    "这个接口上的模型能吃多少 token，直到某个名字有自己的窗口。",
  "settings.models.windowPlaceholder": "来自接口，或留空",
  "settings.models.windows": "上下文窗口",
  "settings.models.windowsHint":
    "这个接口上每个名字自己的上限。发现会在列表带窗口时填入对应的值。",
  "settings.models.windowFor": "{name} 的上下文窗口",
  "settings.models.windowFallback": "其他名字的兜底",
  "settings.models.windowFallbackHint":
    "只在某个名字没有自己的窗口时用。别把某一个模型的上限填在这里。",
  "settings.models.timeout": "请求超时（秒）",
  "settings.models.timeoutHint":
    "流开始后允许连续静默多久。还在输出的调用不会被中断；持续静默的接口会。压缩也使用这个超时。",
  "settings.models.firstByteTimeout": "首字节等待时间（秒）",
  "settings.models.firstByteTimeoutHint":
    "等待模型响应头的时间。模型启动响应较慢时可调大；与流开始后的请求超时独立。0 使用默认值。",

  "settings.aux.title": "辅助模型",
  "settings.aux.desc": "这些活默认跟会话模型走，除非你钉死一个。",
  "settings.aux.titleGen": "生成标题",
  "settings.aux.compact": "压缩摘要",
  "settings.aux.badge": "会话",
  "settings.aux.auto": "自动 · 跟这次会话的模型",
  "settings.aux.automatic": "自动",
  "settings.aux.titleAria": "生成标题所用模型",
  "settings.aux.compactAria": "压缩摘要所用模型",

  "settings.searchEmbed.title": "会话搜索",
  "settings.searchEmbed.desc":
    "关键词搜索一直开启。配置嵌入模型后自动启用语义搜索；清空配置即关闭。",
  "settings.searchEmbed.embedding": "语义搜索",
  "settings.searchEmbed.embeddingHint":
    "配置模型后，⌘K 除了字面匹配还会按意思排序。",
  "settings.searchEmbed.model": "嵌入模型",
  "settings.searchEmbed.modelHint": "只列出服务器标成嵌入的模型。",
  "settings.searchEmbed.modelAria": "嵌入模型",
  "settings.searchEmbed.badge": "搜索",
  "settings.searchEmbed.pick": "选择模型",

}
