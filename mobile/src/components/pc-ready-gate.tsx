import { useEffect, useState, type ReactNode } from "react"

/** Keep the current conversation mounted while its computer reconnects. */
export function PcReadyGate({ waiting, notice, children }: {
  waiting: boolean
  notice: ReactNode
  children: ReactNode
}) {
  const [admitted, setAdmitted] = useState(!waiting)
  useEffect(() => { if (!waiting) setAdmitted(true) }, [waiting])
  return <div className="h-full min-h-0">
    {waiting ? notice : null}
    <div className={waiting ? "hidden" : "h-full min-h-0"} hidden={waiting}
      inert={waiting || undefined} aria-hidden={waiting || undefined}>
      {admitted || !waiting ? children : null}
    </div>
  </div>
}
