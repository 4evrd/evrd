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
  // Many networks and channels stay quick: the view has a fixed height
  // (scroll or drag to pan; only what's in view is painted), each source's
  // strands are one path, and fewer strands are drawn per item when there
  // are many (about STRANDS in all).
  let { left = [], right = [], source, item, leftLabel = '', rightLabel = '', empty = '', strandsPer = 7, single = false } = $props()

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
    const per = Math.max(1, Math.min(strandsPer, Math.floor(STRANDS / Math.max(1, right.length))))
    const byKey = new Map() // `${source}|${dashed}` → path data
    const ds = []
    right.forEach((r, ci) => {
      const ce = box.querySelector(`[data-item="${CSS.escape(String(r.key))}"]`)
      const ne = box.querySelector(`[data-source="${CSS.escape(String(r.source))}"]`)
      if (!ce || !ne || ce.offsetParent === null) return
      const src = left.find((l) => l.id === r.source)
      const nr = ne.getBoundingClientRect(), cr = ce.getBoundingClientRect()
      const x1 = nr.right - b.left, y1 = nr.top - b.top + 34
      const x2 = cr.left - b.left, y2 = cr.top - b.top + cr.height / 2
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
      ds.push({ x: nr.right - b.left, y: nr.top - b.top + 34, color: l.color, big: true })
    })
    paths = [...byKey].map(([k, d]) => { const [src, dashed] = k.split('|'); return { d, source: src, dashed: dashed === '1' } })
    dots = ds
  }

  // drag to pan (mouse); a drag doesn't follow the link it started on
  let vp = $state()
  let drag = null
  let dragged = false
  function down(e) {
    if (e.pointerType !== 'mouse' || e.button !== 0) return
    drag = { x: e.clientX, y: e.clientY, left: vp.scrollLeft, top: vp.scrollTop }
    dragged = false
  }
  function move(e) {
    if (!drag) return
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y
    if (!dragged && Math.hypot(dx, dy) < 5) return
    if (!dragged) { dragged = true; vp.setPointerCapture(e.pointerId); vp.classList.add('panning') }
    vp.scrollTop = drag.top - dy
    vp.scrollLeft = drag.left - dx
  }
  function up(e) {
    if (!drag) return
    drag = null
    vp.classList.remove('panning')
    if (vp.hasPointerCapture?.(e.pointerId)) vp.releasePointerCapture(e.pointerId)
  }
  function click(e) { if (dragged) { e.preventDefault(); e.stopPropagation(); dragged = false } }

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

<!-- svelte-ignore a11y_no_static_element_interactions (mouse panning only; the wheel, touch and keyboard scroll it natively) -->
<div class="topo-viewport" bind:this={vp} onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up} onclickcapture={click}>
<div class="topo" class:single bind:this={box}>
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
