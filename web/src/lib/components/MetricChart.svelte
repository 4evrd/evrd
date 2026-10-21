<script>
  // A small line chart for the admin metrics page: one line per series over
  // evenly spaced points, with a y scale and a hover readout.
  //
  // series: [{ name, points: [number|null …], color? }]   (null: no data there)
  // start:  unix seconds of points[0]; step: seconds between points
  // unit:   shown after values ('ms', …)
  // fancy:  the overview's look, kept exact: curves through every point
  //         that never overshoot (monotone), a soft bloom, and an area under
  //         the first series; dots mark the values under the pointer
  import { netColor, chartMax, date } from '$lib/format.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  let { series = [], start = 0, step = 3600, unit = '', fancy = false } = $props()

  const W = 600, H = 180, PAD = 6
  const n = $derived(Math.max(2, ...series.map((s) => s.points.length)))
  const max = $derived(chartMax(series.flatMap((s) => s.points)))
  const x = (i) => (i / (n - 1)) * W
  const y = (v) => H - PAD - (Math.min(v, max * 1.02) / max) * (H - 2 * PAD)
  // gaps where there's no data break the line
  const path = (pts) => {
    let d = '', pen = false
    pts.forEach((v, i) => {
      if (v == null) { pen = false; return }
      d += `${pen ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`
      pen = true
    })
    return d
  }
  // smooth draws each unbroken run as a monotone cubic (Fritsch–Carlson): it
  // passes through every point and never rises or dips between two of them
  // beyond their values, so the curve shows no peak that isn't in the data
  const smooth = (pts) => {
    let d = ''
    const runs = []
    let run = []
    pts.forEach((v, i) => { if (v == null) { if (run.length) runs.push(run); run = [] } else run.push([x(i), y(v)]) })
    if (run.length) runs.push(run)
    for (const r of runs) {
      d += `M${r[0][0].toFixed(1)},${r[0][1].toFixed(1)}`
      if (r.length === 1) continue
      const h = r.slice(1).map((p, i) => p[0] - r[i][0])
      const dl = r.slice(1).map((p, i) => (p[1] - r[i][1]) / h[i])
      const m = r.map((_, i) => (i === 0 ? dl[0] : i === r.length - 1 ? dl[i - 1] : dl[i - 1] * dl[i] <= 0 ? 0 : (dl[i - 1] + dl[i]) / 2))
      for (let i = 0; i < dl.length; i++) {
        if (dl[i] === 0) { m[i] = 0; m[i + 1] = 0; continue }
        const a = m[i] / dl[i], b = m[i + 1] / dl[i], t2 = a * a + b * b
        if (t2 > 9) { const k = 3 / Math.sqrt(t2); m[i] = k * a * dl[i]; m[i + 1] = k * b * dl[i] }
      }
      for (let i = 0; i < dl.length; i++) {
        const [x0, y0] = r[i], [x1, y1] = r[i + 1], c = h[i] / 3
        d += ` C${(x0 + c).toFixed(1)},${(y0 + m[i] * c).toFixed(1)} ${(x1 - c).toFixed(1)},${(y1 - m[i + 1] * c).toFixed(1)} ${x1.toFixed(1)},${y1.toFixed(1)}`
      }
    }
    return d
  }
  // area under a run (the first series, fancy only)
  const area = (pts) => smooth(pts).split('M').filter(Boolean).map((seg) => {
    const xs = [...seg.matchAll(/(-?[\d.]+),(-?[\d.]+)/g)].map((m) => +m[1])
    return `M${seg} L${Math.max(...xs).toFixed(1)},${H} L${Math.min(...xs).toFixed(1)},${H} Z`
  }).join(' ')
  const lines = $derived(series.map((s, k) => ({ ...s, color: s.color || (k === 0 ? 'var(--ice)' : netColor(k)), d: fancy ? smooth(s.points) : path(s.points) })))
  const fill = $derived(fancy && series[0] ? area(series[0].points) : '')
  const fmt = (v) => (v == null ? '—' : (Math.abs(v) >= 1000 ? (v / 1000).toFixed(v >= 10000 ? 0 : 1) + 'k' : String(Math.round(v * 10) / 10)) + (unit ? ' ' + unit : ''))
  const label = (i) => {
    const d = new Date((start + i * step) * 1000)
    if (step >= 86400) return date(start + i * step + step / 2) // a day: its middle, wherever midnight falls
    return step >= 3600 ? d.toLocaleDateString(i18n.intl, { weekday: 'short' }) + ' ' + d.toTimeString().slice(0, 5) : d.toTimeString().slice(0, 5)
  }
  const ticks = $derived([0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(f * (n - 1))))

  let hover = $state(null)
  function move(e) {
    const r = e.currentTarget.getBoundingClientRect()
    hover = Math.max(0, Math.min(n - 1, Math.round(((e.clientX - r.left) / r.width) * (n - 1))))
  }
</script>

<div class="mchart">
  <div class="legend">
    {#each lines as l}
      <span><i style="--c:{l.color}"></i>{l.name}<b>{fmt(hover != null ? l.points[hover] : l.points.findLast((v) => v != null))}</b></span>
    {/each}
  </div>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="plot" onpointermove={move} onpointerleave={() => (hover = null)}>
    <span class="ymax">{fmt(max)}</span>
    <svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" aria-hidden="true">
      {#each [0.25, 0.5, 0.75] as f}<line x1="0" x2={W} y1={y(max * f)} y2={y(max * f)} class="grid" />{/each}
      {#if fill}
        <defs><linearGradient id="mc-fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0" style="stop-color:{lines[0].color};stop-opacity:.28" /><stop offset="1" style="stop-color:{lines[0].color};stop-opacity:0" /></linearGradient></defs>
        <path d={fill} class="area" />
      {/if}
      {#each lines as l}
        {#if fancy}<path d={l.d} class="bloom" style="stroke:{l.color}" />{/if}
        <path d={l.d} class="line" class:fancy style="stroke:{l.color}" />
      {/each}
      {#if hover != null}<line x1={x(hover)} x2={x(hover)} y1="0" y2={H} class="cursor" />{/if}
    </svg>
    {#if fancy && hover != null}
      {#each lines as l}{#if l.points[hover] != null}<span class="dot" style="left:{(hover / (n - 1)) * 100}%;top:{(y(l.points[hover]) / H) * 100}%;--c:{l.color}"></span>{/if}{/each}
    {/if}
    {#if hover != null}<span class="when" style="left:{(hover / (n - 1)) * 100}%">{label(hover)}</span>{/if}
  </div>
  <div class="axis">{#each ticks as k}<span>{k === n - 1 ? t('format.now') : label(k)}</span>{/each}</div>
</div>

<style>
  .mchart { display: grid; gap: var(--s2); }
  .legend { display: flex; flex-wrap: wrap; gap: var(--s2) var(--s4); font-size: var(--fs-xs); color: var(--muted); min-height: 18px; }
  .legend span { display: inline-flex; align-items: center; gap: 6px; }
  .legend i { width: 10px; height: 2px; border-radius: 2px; background: var(--c); }
  .legend b { font-family: var(--font-mono); font-weight: 500; color: var(--fg); }
  .plot { position: relative; height: 160px; border-radius: 10px; background: var(--bg-deep); border: 1px solid var(--line-soft); overflow: hidden; cursor: crosshair; touch-action: pan-y; }
  .plot svg { width: 100%; height: 100%; display: block; }
  .grid { stroke: var(--line-soft); stroke-width: 1; vector-effect: non-scaling-stroke; }
  .line { fill: none; stroke-width: 1.6; vector-effect: non-scaling-stroke; stroke-linejoin: round; }
  .area { fill: url(#mc-fill); }
  .bloom { fill: none; stroke-width: 7; opacity: 0.16; vector-effect: non-scaling-stroke; stroke-linecap: round; stroke-linejoin: round; }
  .line.fancy { stroke-width: 2; stroke-linecap: round; }
  .dot { position: absolute; width: 8px; height: 8px; margin: -4px 0 0 -4px; border-radius: 50%; background: var(--c); box-shadow: 0 0 0 2px var(--bg-deep), 0 0 10px var(--c); pointer-events: none; }
  .cursor { stroke: var(--muted); stroke-width: 1; stroke-dasharray: 3 3; vector-effect: non-scaling-stroke; }
  .ymax { position: absolute; top: 4px; left: 8px; font-family: var(--font-mono); font-size: 10px; color: var(--faint); pointer-events: none; }
  .when { position: absolute; bottom: 4px; transform: translateX(-50%); padding: 1px 6px; border-radius: 6px; background: var(--glass); font-family: var(--font-mono); font-size: 10px; color: var(--fg); white-space: nowrap; pointer-events: none; }
  .axis { display: flex; justify-content: space-between; font-family: var(--font-mono); font-size: 10px; color: var(--faint); }
</style>
