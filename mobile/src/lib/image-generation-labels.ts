import { getLocale } from "./i18n"

const en = {
  running: "Generating image",
  completed: "Generated image",
  cancelled: "Stopped waiting for the generated image.",
  unknown: "Image generation result could not be confirmed.",
  failed: "Image generation failed.",
  image_unavailable: "Image generation is unavailable for this model source.",
  image_prompt_invalid: "Enter a description after /image.",
  image_text_only: "/image accepts a text description. Remove the attachments and try again.",
  image_permission_denied: "This account can no longer generate images from this source.",
  image_timeout: "Image generation timed out.",
  image_invalid_response: "The gateway did not return a usable image.",
  image_storage_failed: "The generated image could not be saved to this conversation.",
  image_gateway_failed: "The gateway could not generate the image.",
  image_result_unknown: "Image generation result could not be confirmed.",
  image_read_only: "Image generation is unavailable in read-only plan mode.",
  image_limit: "Only one image can be generated per message.",
  loading: "Loading generated image",
  loadFailed: "The generated image could not be loaded.",
  preview: "Open generated image",
}
const zh: typeof en = {
  running: "正在生成图片",
  completed: "生成的图片",
  cancelled: "已停止等待图片。",
  unknown: "未能确认图片生成结果。",
  failed: "图片生成失败。",
  image_unavailable: "当前模型来源无法生成图片。",
  image_prompt_invalid: "请在 /image 后输入图片描述。",
  image_text_only: "/image 仅接受文字描述。请移除附件后重试。",
  image_permission_denied: "当前账号已无法从此来源生成图片。",
  image_timeout: "图片生成超时。",
  image_invalid_response: "网关未返回可用的图片。",
  image_storage_failed: "未能把生成的图片保存到此会话。",
  image_gateway_failed: "网关未能生成图片。",
  image_result_unknown: "未能确认图片生成结果。",
  image_read_only: "只读计划模式下无法生成图片。",
  image_limit: "每条消息最多生成一张图片。",
  loading: "正在加载生成的图片",
  loadFailed: "未能加载生成的图片。",
  preview: "查看大图",
}

export function imageGenerationLabel(code: string): string {
  const catalog = getLocale() === "zh" ? zh : en
  return Object.prototype.hasOwnProperty.call(catalog, code) ? catalog[code as keyof typeof en] : catalog.failed
}
