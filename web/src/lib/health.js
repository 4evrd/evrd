// A network's health page (routes/networks/[id]/health): the range picker,
// what each slot of the charts covers and the uptime band's states. Plain JS
// so Node tests can use it.

// RANGES are the ranges the page offers, as the API takes them.
export const RANGES = ['24h', '7d', '30d']

// rangeOf is ?range= as one of RANGES, 24h when it's anything else.
export const rangeOf = (v) => (RANGES.includes(v) ? v : '24h')

// slotState is how a slot's uptime (the fraction of it connected, null
// when nothing was reported) reads: up, part(ly), down or none; or split
// when, connected, the server was split from the rest of the network for
// some of it (split: that fraction).
export function slotState(v, split = null) {
  if (v == null) return 'none'
  if (v > 0 && split >= 0.005) return 'split'
  if (v >= 0.995) return 'up'
  if (v > 0) return 'part'
  return 'down'
}

// slotTimes is when slot i (of n, step seconds each from start) begins and
// ends, and whether it's the one in progress.
export const slotTimes = (start, step, i, n) => ({ from: start + i * step, to: start + (i + 1) * step, current: i === n - 1 })

// ticks are the slots the x axis labels: about six, on whole days for a
// week, every six hours for a day.
export function ticks(n, step) {
  const every = step >= 86400 ? 5 : n > 48 ? 24 : 4
  const out = []
  for (let i = 0; i <= n; i += every) out.push(i)
  if (out.at(-1) !== n) out.push(n)
  return out
}

// stackTotals is each slot's total over stacked series.
export const stackTotals = (series) => (series[0]?.points || []).map((_, i) => series.reduce((a, s) => a + (s.points[i] || 0), 0))

// peakIndex is the slot with the biggest value (the latest of equals), or
// -1 when every slot is empty.
export function peakIndex(values) {
  let at = -1
  values.forEach((v, i) => { if (v > 0 && (at < 0 || v >= values[at])) at = i })
  return at
}

// uptimeTone is a status tone for an uptime percentage.
export const uptimeTone = (pct) => (pct == null ? '' : pct >= 99 ? 'ok' : pct >= 90 ? 'warn' : 'bad')

// lagTone is a status tone for a lag in ms.
export const lagTone = (ms) => (ms == null ? '' : ms < 500 ? 'ok' : ms < 2000 ? 'warn' : 'bad')

// counted tallies the uptime band's slots by state.
export function counted(uptime, split = []) {
  const out = { up: 0, part: 0, split: 0, down: 0, none: 0 }
  for (const [i, v] of (uptime || []).entries()) out[slotState(v, split?.[i])]++
  return out
}
