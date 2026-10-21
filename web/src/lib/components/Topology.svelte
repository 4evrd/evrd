<script>
  // Two columns joined by bundles of fine strands, after the prototype's
  // "Live topology": sources on the left (networks, or cluster nodes), their
  // items on the right (channels, or accounts). Hovering or focusing a source
  // lights its strands and dims the rest.
  //
  // left:  [{ id, color }]               rendered by the `source` snippet
  // right: [{ key, source, dashed? }]    rendered by the `item` snippet;
  //                                      source is the left id it hangs off
  //
  // Many networks and channels stay quick: the view is a fixed-height map
  // (drag or wheel to pan, ctrl/⌘+wheel or pinch to zoom, + − Fit 100%
  // buttons, arrows and +/− keys); panning and zooming only move one layer,
  // each source's strands are one path, and fewer strands are drawn per
  // item when there are many (about STRANDS in all).
  let { left = [], right = [], source, item, leftLabel = '', rightLabel = '', empty = '', strandsPer = 7, single = false, label = '', zoomLabels = { toolbar: 'Zoom', out: 'Zoom out', in: 'Zoom in', fit: 'Fit' } } = $props()

  const STRANDS = 400
  let box = $state()
  let paths = $state([])  // one per source and style: { d, source, dashed }
  let dots = $state([])   // { x, y, color, big }
  let hl = $state(null)

  // deterministic jitter so strands look organic but never change between redraws
  const rnd = (seed) => { const x = Math.sin(seed * 127.1) * 43758.5453; return x - Math.floor(x) }

  function draw() {
    if (!box) return
    const svg = box.querySelector('.topo-links')
    if (!svg || getComputedStyle(svg).display === 'none') { paths = []; dots = []; return }
    const b = svg.getBoundingClientRect()
    const k = view.k // rects are zoomed; the SVG draws in unzoomed units
    const per = Math.max(1, Math.min(strandsPer, Math.floor(STRANDS / Math.max(1, right.length))))
    const byKey = new Map() // `${source}|${dashed}` → path data
    const ds = []
    right.forEach((r, ci) => {
      const ce = box.querySelector(`[data-item="${CSS.escape(String(r.key))}"]`)
      const ne = box.querySelector(`[data-source="${CSS.escape(String(r.source))}"]`)
      if (!ce || !ne || ce.offsetParent === null) return
      const src = left.find((l) => l.id === r.source)
      const nr = ne.getBoundingClientRect(), cr = ce.getBoundingClientRect()
      const x1 = (nr.right - b.left) / k, y1 = (nr.top - b.top) / k + 34
      const x2 = (cr.left - b.left) / k, y2 = (cr.top - b.top + cr.height / 2) / k
      const k0 = `${r.source}|${r.dashed ? 1 : 0}`
      let d = byKey.get(k0) || ''
      for (let k = 0; k < per; k++) {
        const j = rnd(ci * 13 + k) - 0.5, j2 = rnd(ci * 7 + k * 3) - 0.5
        const sy = y1 + j * 6, ey = y2 + j2 * 8
        const c1x = x1 + (x2 - x1) * (0.35 + j * 0.2), c2x = x1 + (x2 - x1) * (0.6 + j2 * 0.2)
        d += `M${x1},${sy.toFixed(1)}C${c1x.toFixed(1)},${(sy + j * 30).toFixed(1)} ${c2x.toFixed(1)},${(ey + j2 * 24).toFixed(1)} ${x2},${ey.toFixed(1)}`
      }
      byKey.set(k0, d)
      ds.push({ x: x2, y: y2, color: src?.color, big: false })
    })
    left.forEach((l) => {
      const ne = box.querySelector(`[data-source="${CSS.escape(String(l.id))}"]`)
      if (!ne || !right.some((r) => r.source === l.id)) return
      const nr = ne.getBoundingClientRect()
      ds.push({ x: (nr.right - b.left) / k, y: (nr.top - b.top) / k + 34, color: l.color, big: true })
    })
    paths = [...byKey].map(([k, d]) => { const [src, dashed] = k.split('|'); return { d, source: src, dashed: dashed === '1' } })
    dots = ds
  }

  // The map: the topology moves by a transform (x, y in px, k the zoom).
  let vp = $state()
  let view = $state({ x: 0, y: 0, k: 1 })
  const MIN = 0.15, MAX = 2
  // clamp keeps the topology in view: it can't be pushed past an edge, and
  // when smaller than the view it stays inside it
  function clamp(x, y, k) {
    const W = box.offsetWidth * k, H = box.offsetHeight * k, VW = vp.clientWidth, VH = vp.clientHeight
    const cx = (v, c, V) => Math.min(Math.max(v, Math.min(0, V - c)), Math.max(0, V - c))
    return { x: W <= VW ? (VW - W) / 2 : cx(x, W, VW), y: cx(y, H, VH), k }
  }
  // fitK is the zoom that shows all of it; zooming out goes at least that far
  const fitK = () => Math.min(1, vp.clientWidth / box.offsetWidth, vp.clientHeight / box.offsetHeight)
  const bound = (k) => Math.min(MAX, Math.max(Math.min(MIN, fitK()), k))
  function set(x, y, k) { view = clamp(x, y, bound(k)) }
  // zoomAt zooms by f keeping the point (px, py) of the view where it is
  function zoomAt(f, px = vp.clientWidth / 2, py = vp.clientHeight / 2) {
    const k = bound(view.k * f)
    set(px - ((px - view.x) * k) / view.k, py - ((py - view.y) * k) / view.k, k)
  }
  const fit = () => set(0, 0, fitK())
  const reset = () => set(0, view.y, 1)

  // pointers: one drags, two pinch; a drag doesn't follow the link it started on
  const ptrs = new Map()
  let drag = null
  let pinch = null
  let dragged = false
  function down(e) {
    if (e.target.closest('.topo-zoom') || (e.pointerType === 'mouse' && e.button !== 0)) return
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (ptrs.size === 1) { drag = { x: e.clientX, y: e.clientY, vx: view.x, vy: view.y }; dragged = false }
    if (ptrs.size === 2) {
      const [a, b] = [...ptrs.values()]
      pinch = { d: Math.hypot(a.x - b.x, a.y - b.y), k: view.k }
      drag = null
    }
  }
  function move(e) {
    if (!ptrs.has(e.pointerId)) return
    ptrs.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (pinch && ptrs.size === 2) {
      const [a, b] = [...ptrs.values()], r = vp.getBoundingClientRect()
      zoomAt((pinch.k * Math.hypot(a.x - b.x, a.y - b.y)) / pinch.d / view.k, (a.x + b.x) / 2 - r.left, (a.y + b.y) / 2 - r.top)
      return
    }
    if (!drag) return
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y
    if (!dragged && Math.hypot(dx, dy) < 5) return
    if (!dragged) { dragged = true; vp.setPointerCapture(e.pointerId); vp.classList.add('panning') }
    set(drag.vx + dx, drag.vy + dy, view.k)
  }
  function up(e) {
    ptrs.delete(e.pointerId)
    if (ptrs.size < 2) pinch = null
    if (ptrs.size === 0) drag = null
    vp.classList.remove('panning')
    if (vp.hasPointerCapture?.(e.pointerId)) vp.releasePointerCapture(e.pointerId)
  }
  function click(e) { if (dragged) { e.preventDefault(); e.stopPropagation(); dragged = false } }

  // wheel: ctrl/⌘ (and trackpad pinch) zooms at the cursor; otherwise it
  // pans, and at an edge lets the page scroll on
  function wheel(e) {
    const r = vp.getBoundingClientRect()
    if (e.ctrlKey || e.metaKey) {
      e.preventDefault()
      zoomAt(Math.exp(-e.deltaY * 0.0025), e.clientX - r.left, e.clientY - r.top)
      return
    }
    const next = clamp(view.x - e.deltaX, view.y - e.deltaY, view.k)
    if (next.x === view.x && next.y === view.y) return // at the edge: the page scrolls
    e.preventDefault()
    view = next
  }
  function key(e) {
    if (e.target !== vp) return
    const step = 60, map = { ArrowUp: [0, step], ArrowDown: [0, -step], ArrowLeft: [step, 0], ArrowRight: [-step, 0] }
    if (map[e.key]) { e.preventDefault(); set(view.x + map[e.key][0], view.y + map[e.key][1], view.k) }
    else if (e.key === '+' || e.key === '=') { e.preventDefault(); zoomAt(1.25) }
    else if (e.key === '-') { e.preventDefault(); zoomAt(0.8) }
    else if (e.key === '0') { e.preventDefault(); reset() }
  }
  // tabbing to a card brings it into view
  function focusin(e) {
    const el = e.target.closest('[data-source], [data-item]')
    if (!el) return
    const r = el.getBoundingClientRect(), v = vp.getBoundingClientRect()
    let { x, y } = view
    if (r.top < v.top) y += v.top - r.top + 12
    else if (r.bottom > v.bottom) y -= r.bottom - v.bottom + 12
    if (r.left < v.left) x += v.left - r.left + 12
    else if (r.right > v.right) x -= r.right - v.right + 12
    set(x, y, view.k)
  }
  // a change in size (search, filter, resize) keeps the view in bounds
  $effect(() => {
    if (!box || !vp) return
    const ro = new ResizeObserver(() => { view = clamp(view.x, view.y, view.k) })
    ro.observe(box)
    ro.observe(vp)
    return () => ro.disconnect()
  })

  $effect(() => {
    left; right
    if (!box) return
    const ro = new ResizeObserver(() => requestAnimationFrame(draw))
    ro.observe(box)
    requestAnimationFrame(draw)
    document.fonts?.ready.then(draw)
    return () => ro.disconnect()
  })

  const over = (e) => { const s = e.target.closest('[data-source]'); hl = s ? s.dataset.source : (e.target.closest('[data-item]')?.dataset.from ?? null) }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (a pannable map: arrows and +/− move it) -->
<div class="topo-viewport" bind:this={vp} tabindex="0" role="region" aria-label={label} onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up} onclickcapture={click} onwheel={wheel} onkeydown={key} onfocusin={focusin}>
<div class="topo-zoom" role="toolbar" aria-label={zoomLabels.toolbar}>
  <button type="button" class="icon-btn" aria-label={zoomLabels.out} onclick={() => zoomAt(0.8)}>−</button>
  <span class="topo-k" aria-live="polite">{Math.round(view.k * 100)}%</span>
  <button type="button" class="icon-btn" aria-label={zoomLabels.in} onclick={() => zoomAt(1.25)}>+</button>
  <button type="button" class="btn btn-ghost btn-sm" onclick={fit}>{zoomLabels.fit}</button>
  <button type="button" class="btn btn-ghost btn-sm" onclick={reset}>100%</button>
</div>
<div class="topo" class:single bind:this={box} style="transform:translate({view.x}px,{view.y}px) scale({view.k})">
  <svg class="topo-links" aria-hidden="true">
    <defs>
      {#each left as l, i}
        <linearGradient id="tg-{i}-{l.id}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" style="stop-color:{l.color}" /><stop offset="1" style="stop-color:var(--ice);stop-opacity:.55" /></linearGradient>
      {/each}
    </defs>
    {#each paths as p (p.source + '|' + p.dashed)}
      {@const i = left.findIndex((l) => String(l.id) === p.source)}
      <path d={p.d} class:hl={hl != null && p.source === String(hl)} class:dim={hl != null && p.source !== String(hl)}
        style="stroke:url(#tg-{i}-{p.source}){p.dashed ? ';stroke-dasharray:3 4' : ''}" />
    {/each}
    {#each dots as d}
      {#if d.big}<circle cx={d.x} cy={d.y} r="7" class="halo" style="fill:{d.color}" />{/if}
      <circle cx={d.x} cy={d.y} r={d.big ? 3.5 : 2.5} style="fill:{d.color}" />
    {/each}
  </svg>
  <div class="topo-col" role="group" onpointerover={over} onpointerleave={() => (hl = null)} onfocusin={over} onfocusout={() => (hl = null)}>
    {#if leftLabel}<span class="caps">{leftLabel}</span>{/if}
    {#each left as l (l.id)}
      <div data-source={l.id} class="topo-src" class:active={hl != null && String(hl) === String(l.id)}>{@render source(l, hl != null && String(hl) !== String(l.id))}</div>
    {/each}
  </div>
  <div aria-hidden="true"></div>
  <div class="topo-col right" role="group" onpointerover={over} onpointerleave={() => (hl = null)}>
    {#if rightLabel}<span class="caps">{rightLabel}</span>{/if}
    {#each right as r (r.key)}
      <div data-item={r.key} data-from={r.source} class="topo-item">{@render item(r, hl != null && String(hl) !== String(r.source))}</div>
    {:else}
      {#if empty}<p class="desc">{empty}</p>{/if}
    {/each}
  </div>
</div>
</div>

<style>
  .topo-src, .topo-item { display: grid; min-width: 0; }
</style>
