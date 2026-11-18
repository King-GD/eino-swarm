export const imageGenerationEn = {
  "imageGen.running": "Generating image…",
  "imageGen.image": "Generated image",
  "imageGen.save": "Save image",
  "imageGen.loading": "Loading generated image…",
  "imageGen.loadFailed": "Could not load the generated image.",
  "imageGen.failed": "Image generation failed.",
  "imageGen.cancelled": "Image generation stopped.",
  "imageGen.unknown": "Image generation result could not be confirmed.",
  "imageGen.unavailable": "Image generation is unavailable for this model source.",
  "imageGen.accessDenied": "The gateway denied this image request.",
  "imageGen.timeout": "The image request timed out.",
  "imageGen.invalidResult": "The gateway returned an invalid image.",
  "imageGen.saveFailed": "Could not save the generated image.",
  "imageGen.invalidPrompt": "Enter a valid description after /image.",
  "imageGen.textOnly": "/image accepts text only.",
  "imageGen.readOnly": "Image generation is unavailable in read-only plan mode.",
} as const

export const imageGenerationZh: Record<keyof typeof imageGenerationEn, string> = {
  "imageGen.running": "正在生成图片…",
  "imageGen.image": "生成的图片",
  "imageGen.save": "保存图片",
  "imageGen.loading": "正在加载生成的图片…",
  "imageGen.loadFailed": "无法加载生成的图片。",
  "imageGen.failed": "图片生成失败。",
  "imageGen.cancelled": "已停止生成图片。",
  "imageGen.unknown": "生图结果未确认。",
  "imageGen.unavailable": "当前模型来源暂无可用生图能力。",
  "imageGen.accessDenied": "网关拒绝了本次生图请求。",
  "imageGen.timeout": "生图请求超时。",
  "imageGen.invalidResult": "网关返回的图片无效。",
  "imageGen.saveFailed": "无法保存生成的图片。",
  "imageGen.invalidPrompt": "请在 /image 后输入有效的图片描述。",
  "imageGen.textOnly": "/image 仅接受文字描述。",
  "imageGen.readOnly": "只读计划模式下无法生成图片。",
}

export function imageGenerationErrorKey(code?: string): keyof typeof imageGenerationEn | undefined {
  switch (code) {
    case "image_generation_unavailable": return "imageGen.unavailable"
    case "image_generation_access_denied": return "imageGen.accessDenied"
    case "image_generation_timeout": return "imageGen.timeout"
    case "image_generation_invalid_result": return "imageGen.invalidResult"
    case "image_generation_save_failed": return "imageGen.saveFailed"
    case "image_generation_invalid_prompt": return "imageGen.invalidPrompt"
    case "image_generation_text_only": return "imageGen.textOnly"
    case "image_generation_read_only": return "imageGen.readOnly"
    default: return undefined
  }
}
