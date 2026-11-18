export function gatewayImageCode(code?: string): string | undefined {
  switch (code) {
    case "image_generation_unavailable": return "image_unavailable"
    case "image_generation_access_denied": return "image_permission_denied"
    case "image_generation_timeout": return "image_timeout"
    case "image_generation_invalid_result": return "image_invalid_response"
    case "image_generation_save_failed": return "image_storage_failed"
    case "image_generation_failed": return "image_gateway_failed"
    case "image_generation_unknown": return "image_result_unknown"
    case "image_generation_invalid_prompt": return "image_prompt_invalid"
    case "image_generation_text_only": return "image_text_only"
    case "image_generation_read_only": return "image_read_only"
    default: return undefined
  }
}
