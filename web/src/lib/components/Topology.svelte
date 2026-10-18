<script>
  // Two columns joined by bundles of fine strands, after the prototype's
  // "Live topology": sources on the left (networks, or cluster nodes), their
  // items on the right (channels, or accounts). Hovering or focusing a source
  // lights its strands and dims the rest.
  //
  // left:  [{ id, color }]               rendered by the `source` snippet
  // right: [{ key, source, dashed? }]    rendered by the `item` snippet;
  //                                      source is the left id it hangs off
  let { left = [], right = [], source, item, leftLabel = '', rightLabel = '', empty = '', strandsPer = 7, single = false } = $props()

  let box = $state()
  let paths = $state([])  // { d, source, color, dashed }
  let dots = $state([])   // { x, y, color, big }
  let hl = $state(null)

  // deterministic jitter so strands look organic but never change between redraws
  const rnd = (seed) => { const x = Math.sin(seed * 127.1) * 43758.5453; return x - Math.floor(x) }

  function draw() {
    if (!box) return
    const svg = box.querySelector('.topo-links')
    if (!svg || getComputedStyle(svg).display === 'none') { paths = []; dots = []; return }
    const b = svg.getBoundingClientRect()
    const ps = [], ds = []
    right.forEach((r, ci) => {
      const ce = box.querySelector(`[data-item="${CSS.escape(String(r.key))}"]`)
      const ne = box.querySelector(`[data-source="${CSS.escape(String(r.source))}"]`)
      if (!ce || !ne || ce.offsetParent === null) return
      const src = left.find((l) => l.id === r.source)
      const nr = ne.getBoundingClientRect(), cr = ce.getBoundingClientRect()
      const x1 = nr.right - b.left, y1 = nr.top - b.top + 34
      const x2 = cr.left - b.left, y2 = cr.top - b.top + cr.height / 2
      for (let k = 0; k < strandsPer; k++) {
        const j = rnd(ci * 13 + k) - 0.5, j2 = rnd(ci * 7 + k * 3) - 0.5
        const sy = y1 + j * 6, ey = y2 + j2 * 8
        const c1x = x1 + (x2 - x1) * (0.35 + j * 0.2), c2x = x1 + (x2 - x1) * (0.6 + j2 * 0.2)
        ps.push({ d: `M${x1},${sy.toFixed(1)} C${c1x.toFixed(1)},${(sy + j * 30).toFixed(1)} ${c2x.toFixed(1)},${(ey + j2 * 24).toFixed(1)} ${x2},${ey.toFixed(1)}`, source: r.source, color: src?.color, dashed: r.dashed })
      }
      ds.push({ x: x2, y: y2, color: src?.color, big: false })
    })
    left.forEach((l) => {
      const ne = box.querySelector(`[data-source="${CSS.escape(String(l.id))}"]`)
      if (!ne || !right.some((r) => r.source === l.id)) return
      const nr = ne.getBoundingClientRect()
      ds.push({ x: nr.right - b.left, y: nr.top - b.top + 34, color: l.color, big: true })
    })
    paths = ps
    dots = ds
  }

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

<div class="topo" class:single bind:this={box}>
  <svg class="topo-links" aria-hidden="true">
    <defs>
      {#each left as l, i}
        <linearGradient id="tg-{i}-{l.id}" x1="0" y1="0" x2="1" y2="0"><stop offset="0" style="stop-color:{l.color}" /><stop offset="1" style="stop-color:var(--ice);stop-opacity:.55" /></linearGradient>
      {/each}
    </defs>
    {#each paths as p}
      {@const i = left.findIndex((l) => l.id === p.source)}
      <path d={p.d} class:hl={hl != null && String(p.source) === String(hl)} class:dim={hl != null && String(p.source) !== String(hl)}
        style="stroke:url(#tg-{i}-{p.source}){p.dashed ? ';stroke-dasharray:3 4' : ''}" />
    {/each}
    {#each dots as d}
      <circle cx={d.x} cy={d.y} r={d.big ? 3.5 : 2.5} style="fill:{d.color}{d.big ? `;filter:drop-shadow(0 0 4px ${d.color})` : ''}" />
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

<style>
  .topo-src, .topo-item { display: grid; min-width: 0; }
</style>
