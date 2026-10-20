<script>
  // A small line chart for the admin metrics page: one line per series over
  // evenly spaced points, with a y scale and a hover readout.
  //
  // series: [{ name, points: [number|null …] }]   (null: no data there)
  // start:  unix seconds of points[0]; step: seconds between points
  // unit:   shown after values ('ms', …)
  import { netColor, chartMax } from '$lib/format.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  let { series = [], start = 0, step = 3600, unit = '' } = $props()

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
  const lines = $derived(series.map((s, k) => ({ ...s, color: k === 0 ? 'var(--ice)' : netColor(k), d: path(s.points) })))
  const fmt = (v) => (v == null ? '—' : (Math.abs(v) >= 1000 ? (v / 1000).toFixed(v >= 10000 ? 0 : 1) + 'k' : String(Math.round(v * 10) / 10)) + (unit ? ' ' + unit : ''))
  const label = (i) => {
    const d = new Date((start + i * step) * 1000)
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
      {#each lines as l}
        <path d={l.d} class="line" style="stroke:{l.color}" />
      {/each}
      {#if hover != null}<line x1={x(hover)} x2={x(hover)} y1="0" y2={H} class="cursor" />{/if}
    </svg>
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
  .cursor { stroke: var(--muted); stroke-width: 1; stroke-dasharray: 3 3; vector-effect: non-scaling-stroke; }
  .ymax { position: absolute; top: 4px; left: 8px; font-family: var(--font-mono); font-size: 10px; color: var(--faint); pointer-events: none; }
  .when { position: absolute; bottom: 4px; transform: translateX(-50%); padding: 1px 6px; border-radius: 6px; background: var(--glass); font-family: var(--font-mono); font-size: 10px; color: var(--fg); white-space: nowrap; pointer-events: none; }
  .axis { display: flex; justify-content: space-between; font-family: var(--font-mono); font-size: 10px; color: var(--faint); }
</style>
