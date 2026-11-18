import { render } from "@testing-library/react"
import { vi } from "vitest"
import { Composer } from "@/components/app/composer"
import { TooltipProvider } from "@/components/ui/tooltip"
import type { Attachment, ModelInfo } from "@/lib/types"

export function composerElement(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const models: ModelInfo[] = [
    {
      id: "default\tmock", provider_id: "default", provider_label: "Offline",
      label: "mock", model: "m", ready: true, default: true,
    },
  ]
  return (
    <TooltipProvider>
      <Composer
        running={false}
        models={models}
        provider="default"
        model="m"
        onModelChange={vi.fn()}
        reasoning=""
        reasoningLevels={["low", "medium", "high"]}
        onReasoningChange={vi.fn()}
        onSend={vi.fn()}
        onStop={vi.fn()}
        onUpload={vi.fn(async (): Promise<Attachment[]> => [])}
        focusSignal={0}
        {...props}
      />
    </TooltipProvider>
  )
}

export function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  return render(composerElement(props))
}
