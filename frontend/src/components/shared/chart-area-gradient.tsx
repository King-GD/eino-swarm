export function ChartAreaGradient({ id, color, values = [] }: {
  id: string
  color: string
  values?: readonly number[]
}) {
  const top = Math.max(0, ...values)
  const bottom = Math.min(0, ...values)
  // Signed series fade toward zero from either side of the baseline.
  const baseline = top === bottom ? 1 : top / (top - bottom)
  return (
    <linearGradient id={id} x1="0%" x2="0%" y1="0%" y2="100%">
      {baseline > 0 ? <stop offset={0} stopColor={color} stopOpacity={0.1} /> : null}
      <stop offset={baseline} stopColor={color} stopOpacity={0} />
      {baseline < 1 ? <stop offset={1} stopColor={color} stopOpacity={0.1} /> : null}
    </linearGradient>
  )
}
