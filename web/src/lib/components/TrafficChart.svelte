<script>
  // Lines per hour per network over 24h, drawn as the prototype's strand
  // bundles: each network is a soft area, a bloom, fine jittered strands and a
  // bright core. Markers tag events (link lost, netsplit, client attached).
  //
  // series:  [{ name, color, points: [25 numbers] }]   (first is drawn brightest)
  // start:   unix seconds of points[0]; step is an hour
  // markers: [{ at: unix, label, tone: 'bad'|'warn'|'ok' }]
  // focus:   a series' name to bring forward (the others dim)
  // Hovering shows the hour under the pointer: each network's lines and the
  // events in it. Marker labels stack in rows rather than overlap.
  import { hourAt, hourSpan, hourRows, lanes } from '$lib/format.js'

  let { series = [], start = 0, markers = [], focus = null } = $props()
  let width = $state(0)
  let hover = $state(null) // the point index under the pointer

  const W = 1000, H = 240
  const rnd = (seed) => { const x = Math.sin(seed * 127.1) * 43758.5453; return x - Math.floor(x) }
  const max = $derived.by(() => {
    let m = 0
    for (const s of series) for (const v of s.points) m = Math.max(m, v)
    if (m <= 0) return 10
    const p = Math.pow(10, Math.floor(Math.log10(m)))
    for (const k of [1, 2, 2.5, 5, 10]) if (k * p >= m * 1.1) return k * p
    return 10 * p
  })
  const n = $derived(series[0]?.points.length || 25)
  const pt = (v, i) => [(i / (n - 1)) * W, H - (v / max) * (H - 20)]
  // smooth Catmull-Rom -> cubic Bézier so strands curve like fibre
  const smooth = (pts) => pts.reduce((d, p, i, a) => {
    if (!i) return `M${p[0].toFixed(1)},${p[1].toFixed(1)}`
    const p0 = a[i - 2] || a[i - 1], p1 = a[i - 1], p3 = a[i + 1] || p
    const c1 = [p1[0] + (p[0] - p0[0]) / 6, p1[1] + (p[1] - p0[1]) / 6], c2 = [p[0] - (p3[0] - p1[0]) / 6, p[1] - (p3[1] - p1[1]) / 6]
    return d + ` C${c1[0].toFixed(1)},${c1[1].toFixed(1)} ${c2[0].toFixed(1)},${c2[1].toFixed(1)} ${p[0].toFixed(1)},${p[1].toFixed(1)}`
  }, '')
  const drawn = $derived(series.map((se, k) => {
    const core = se.points.map(pt)
    const d = smooth(core)
    const strands = []
    const count = k ? 7 : 12
    for (let j = 0; j < count; j++) {
      const amp = (k ? 10 : 16) * (0.4 + rnd(k * 50 + j))
      const ph = rnd(k * 31 + j) * 6
      strands.push({ d: smooth(se.points.map((v, i) => { const [x, y] = pt(v, i); return [x, y + (v > 0 ? Math.sin(i * 0.9 + ph) * amp * (0.4 + rnd(j * 24 + i)) : 0)] })), op: (0.25 + rnd(j + k * 9) * 0.35).toFixed(2) })
    }
    return { ...se, d, strands, k }
  }))
  const axis = $derived.by(() => {
    if (!start) return []
    return [0, 4, 8, 12, 16, 20, 24].map((h) => (h === 24 ? 'now' : new Date((start + h * 3600) * 1000).toTimeString().slice(0, 5)))
  })
  // a label is about 7px a character in the mono caption, plus its swatch
  const marks = $derived(lanes(markers.filter((m) => m.at >= start && m.at <= start + 24 * 3600 + 3600).map((m) => {
    const left = Math.min(100, ((m.at - start) / (24 * 3600)) * 100)
    return { ...m, left, x: (left / 100) * width, w: m.label.length * 7 + 30 }
  })))
  const move = (e) => {
    const r = e.currentTarget.getBoundingClientRect()
    hover = hourAt((e.clientX - r.left) / r.width, n)
  }
  const rows = $derived(hover == null ? [] : hourRows(series, hover))
  // the events in the hovered hour: point i covers the hour ending at start+i h
  const hourMarks = $derived(hover == null ? [] : marks.filter((m) => m.at > start + (hover - 1) * 3600 && m.at <= start + hover * 3600))
</script>

<div class="chart">
  <div class="chart-plot">
    <svg viewBox="0 0 {W} {H}" preserveAspectRatio="none" aria-hidden="true">
      <defs>
        {#each drawn as se}
          <linearGradient id="fa{se.k}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" style="stop-color:{se.color};stop-opacity:{se.k ? 0.06 : 0.16}" /><stop offset="1" style="stop-color:{se.color};stop-opacity:0" /></linearGradient>
          <linearGradient id="fs{se.k}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" style="stop-color:var(--signal)" /><stop offset=".35" style="stop-color:{se.color}" /><stop offset="1" style="stop-color:{se.color}" /></linearGradient>
        {/each}
      </defs>
      {#each drawn as se}
        <g class="series" class:dim={focus && focus !== se.name}>
        <path d="{se.d} L{W},{H} L0,{H} Z" style="fill:url(#fa{se.k})" />
        <path class="bloom" d={se.d} style="stroke:{se.color}" />
        {#each se.strands as st}<path class="strand" d={st.d} style="stroke:url(#fs{se.k});stroke-opacity:{st.op}" />{/each}
        <path class="core" d={se.d} style="stroke:{se.k ? se.color : 'var(--ice)'}" />
        </g>
      {/each}
    </svg>
    {#each marks as m}
      <div class="marker {m.tone}" class:flip={m.left > 80} class:pin={m.lane < 0} style="left:{m.left}%;--lane:{Math.max(m.lane, 0)}"><span class="tag">{m.label}</span></div>
    {/each}
    <!-- the hover readout; the series are also in the legend and the networks list -->
    <div class="chart-hover" role="presentation" bind:clientWidth={width} onpointermove={move} onpointerleave={() => (hover = null)}>
      {#if hover != null}
        {@const left = (hover / (n - 1)) * 100}
        <div class="guide" style="left:{left}%"></div>
        <div class="readout" class:flip={left > 60} style="left:{left}%" data-testid="chart-readout">
          <div class="readout-h">{hourSpan(start, hover, n)}</div>
          {#each rows as r}
            <div class="readout-row" class:zero={!r.value}><i style="--c:{r.color}"></i><span>{r.name}</span><b>{r.value}</b></div>
          {/each}
          {#each hourMarks as m}<div class="readout-ev {m.tone}">{m.label}</div>{/each}
        </div>
      {/if}
    </div>
  </div>
  <div class="axis-x" aria-hidden="true">{#each axis as t}<span>{t}</span>{/each}</div>
</div>
