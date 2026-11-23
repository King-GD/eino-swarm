import type { ReactNode } from "react"

import { Dialog, DialogContent } from "@/components/ui/dialog"

export function WidePreviewDialog({ children, testId, onClose, onReturnFocus }: {
  children: ReactNode
  testId: string
  onClose: () => void
  onReturnFocus: () => void
}) {
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        data-testid={testId}
        className="flex h-[85dvh] w-[calc(100%-2rem)] max-w-5xl flex-col gap-0 overflow-hidden p-0 data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-1/2 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-1/2"
        onCloseAutoFocus={(event) => { event.preventDefault(); onReturnFocus() }}
      >
        {children}
      </DialogContent>
    </Dialog>
  )
}
