export function ReviewProgressBar({ label, percent }: { label: string; percent?: number }) {
  return (
    <div
      className="h-1 overflow-hidden rounded-full bg-muted"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
    >
      {percent === undefined ? (
        <div className="h-full w-1/3 rounded-full bg-primary motion-safe:animate-pulse" />
      ) : (
        <div
          className="h-full bg-primary motion-safe:transition-[width]"
          style={{ width: `${percent}%`, minWidth: percent > 0 ? 1 : undefined }}
        />
      )}
    </div>
  )
}
