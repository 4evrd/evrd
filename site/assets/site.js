// evrd public site — shared header/footer, icons and interaction helpers.
// Each page is plain HTML with <body class="site" data-page="…"> and a <main id="main">;
// this file adds the site header + footer around it and wires the shared controls.

// One outline icon family (24px grid, 1.6 stroke) — same set as the console (web/src/lib/icons.js).
const ICONS = {
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18a14 14 0 0 1 0-18"/>',
  server: '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  key: '<circle cx="7.5" cy="15.5" r="4"/><path d="M10.5 12.5 20 3M16 7l3 3M14 9l2 2"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14a6.5 6.5 0 0 1 3.5 6"/>',
  menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a1 1 0 0 1 1-1h9"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  activity: '<path d="M3 12h4l3-7 4 14 3-7h4"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  alert: '<path d="M12 3.5 2.5 20h19z"/><path d="M12 10v4M12 17h.01"/>',
  refresh: '<path d="M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5"/>',
  chevron: '<path d="m9 6 6 6-6 6"/>',
  shield: '<path d="M12 3 19.5 6v6c0 4.6-3.2 7.8-7.5 9-4.3-1.2-7.5-4.4-7.5-9V6z"/><path d="m9 12 2 2 4-4"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7.5a4 4 0 0 1 8 0V11"/>',
  hash: '<path d="M5 9h14M5 15h14M10 4 8 20M16 4l-2 16"/>',
  chart: '<path d="M4 4v16h16"/><path d="m7 14 4-4 3 3 5-6"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4.2-4.2"/>',
  disk: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  message: '<path d="M4 5.5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H9l-5 4z"/>',
  plug: '<path d="M9 3v5M15 3v5M6 8h12v3a6 6 0 0 1-12 0zM12 17v4"/>',
  monitor: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
  sliders: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
  terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/>',
  external: '<path d="M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5"/>',
  power: '<path d="M12 3v8"/><path d="M6.3 6.8a8 8 0 1 0 11.4 0"/>',
  download: '<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',
  book: '<path d="M4 5a2 2 0 0 1 2-2h13v15H6a2 2 0 0 0-2 2z"/><path d="M4 20a2 2 0 0 0 2 2h13v-4"/>',
  box: '<path d="M3.5 7.5 12 3l8.5 4.5v9L12 21l-8.5-4.5z"/><path d="M3.5 7.5 12 12l8.5-4.5M12 12v9"/>',
  code: '<path d="m8 7-5 5 5 5M16 7l5 5-5 5M14 4l-4 16"/>',
  play: '<path d="M8 5.5v13l10.5-6.5z"/>',
  pause: '<path d="M8.5 5v14M15.5 5v14"/>',
  chevronLeft: '<path d="m15 6-6 6 6 6"/>',
  layers: '<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>',
  route: '<circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M8.5 18H15a3.5 3.5 0 0 0 0-7H9a3.5 3.5 0 0 1 0-7h6.5"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/>',
  balance: '<path d="M12 4v16M5 20h14M6 8h12"/><path d="m6 8-3 6a3 3 0 0 0 6 0zM18 8l-3 6a3 3 0 0 0 6 0z"/>',
  git: '<circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="9" r="2.5"/><path d="M6 8.5v7M18 11.5c0 3-3 4-6 4.5H8.5"/>',
}
function icon(name, cls = '') {
  return `<svg class="i ${cls}" viewBox="0 0 24 24" aria-hidden="true" focusable="false">${ICONS[name] || ''}</svg>`
}
function hydrateIcons(root = document) {
  root.querySelectorAll('i[data-icon]').forEach((el) => { el.outerHTML = icon(el.dataset.icon, el.className) })
}

const REPO = 'https://github.com/4evrd/evrd'

// ---------------------------------------------------------------------------
// Header + footer
function shell() {
  const page = document.body.dataset.page
  const nav = [
    { id: 'home', href: 'index.html#features', label: 'Features' },
    { id: 'architecture', href: 'architecture.html', label: 'Architecture' },
    { id: 'docs', href: 'docs.html', label: 'Docs' },
    { id: 'download', href: 'download.html', label: 'Download' },
  ]
  const head = document.createElement('header')
  head.className = 'site-head'
  head.innerHTML = `
    <a class="skip" href="#main">Skip to content</a>
    <div class="wrap">
      <a class="brand" href="index.html" aria-label="evrd home"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></a>
      <button class="icon-btn head-toggle" type="button" aria-label="Open menu" aria-controls="site-nav" aria-expanded="false" id="head-toggle">${icon('menu')}</button>
      <nav class="site-nav" id="site-nav" aria-label="Main">
        ${nav.map((n) => `<a class="nav-a" href="${n.href}"${n.id === page && n.id !== 'home' ? ' aria-current="page"' : ''}>${n.label}</a>`).join('')}
        <a class="nav-a" href="${REPO}" rel="noopener">GitHub</a>
        <a class="btn btn-primary" href="download.html#install">${icon('download')}Get evrd</a>
      </nav>
    </div>`
  document.body.prepend(head)

  const foot = document.createElement('footer')
  foot.className = 'site-foot'
  foot.innerHTML = `
    <div class="wrap">
      <div class="about">
        <a class="brand" href="index.html"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></a>
        <p>The IRCForever bouncer — stay connected, forever. Written in Go, with a Svelte web client.</p>
      </div>
      <div class="foot-col"><span class="caps">Product</span><a href="index.html#features">Features</a><a href="index.html#how">How it works</a><a href="architecture.html">Architecture</a><a href="download.html">Download</a></div>
      <div class="foot-col"><span class="caps">Docs</span><a href="docs.html#quick-start">Quick start</a><a href="docs.html#native-clients">Native clients</a><a href="docs.html#status">*status commands</a><a href="docs.html#clustering">Clustering</a></div>
      <div class="foot-col"><span class="caps">Project</span><a href="${REPO}" rel="noopener">Source on GitHub</a><a href="${REPO}/releases" rel="noopener">Releases</a><a href="${REPO}/pkgs/container/evrd" rel="noopener">Container images</a><a href="${REPO}/blob/main/LICENSE" rel="noopener">License (MIT)</a></div>
      <div class="foot-base"><span>evrd · MIT licensed · github.com/4evrd/evrd</span><span>Released with semantic-release · versions follow Conventional Commits</span></div>
    </div>`
  document.body.append(foot)

  const toggle = head.querySelector('#head-toggle')
  const sn = head.querySelector('#site-nav')
  const setOpen = (open) => {
    sn.classList.toggle('open', open)
    toggle.setAttribute('aria-expanded', String(open))
    toggle.setAttribute('aria-label', open ? 'Close menu' : 'Open menu')
    toggle.innerHTML = icon(open ? 'x' : 'menu')
  }
  toggle.addEventListener('click', () => setOpen(!sn.classList.contains('open')))
  sn.addEventListener('click', (e) => { if (e.target.closest('a')) setOpen(false) })
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && sn.classList.contains('open')) { setOpen(false); toggle.focus() } })
}

// ---------------------------------------------------------------------------
// Toasts
let toastBox
function toast(msg, kind = 'ok') {
  if (!toastBox) { toastBox = document.createElement('div'); toastBox.className = 'toasts'; toastBox.setAttribute('role', 'status'); toastBox.setAttribute('aria-live', 'polite'); document.body.appendChild(toastBox) }
  const t = document.createElement('div')
  t.className = 'toast ' + kind
  t.innerHTML = `${icon(kind === 'bad' ? 'alert' : 'check')}<span>${msg}</span>`
  toastBox.appendChild(t)
  setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 200) }, 3000)
}

// Copy buttons — data-copy="text", or data-copy-from="#id" to copy an element's text.
function wireCopy() {
  document.addEventListener('click', async (e) => {
    const b = e.target.closest('[data-copy], [data-copy-from]'); if (!b) return
    const text = b.dataset.copy ?? document.querySelector(b.dataset.copyFrom)?.innerText ?? ''
    let ok = true
    try { await navigator.clipboard.writeText(text.trim()) } catch { ok = false }
    if (!ok) { toast('Your browser blocked the clipboard — select the text and copy it instead.', 'bad'); return }
    const old = b.innerHTML
    b.innerHTML = icon('check'); b.setAttribute('aria-label', 'Copied')
    toast('Copied to clipboard')
    setTimeout(() => { b.innerHTML = old; b.setAttribute('aria-label', b.dataset.label || 'Copy') }, 1400)
  })
}

// Tabs — role="tablist" with role="tab" aria-controls; arrow keys move; data-hash keeps the URL in step.
function wireTabs(root = document) {
  root.querySelectorAll('[role="tablist"]').forEach((list) => {
    const tabs = [...list.querySelectorAll('[role="tab"]')]
    const select = (t, focus, push) => {
      tabs.forEach((x) => { const on = x === t; x.setAttribute('aria-selected', String(on)); x.tabIndex = on ? 0 : -1 })
      tabs.forEach((x) => { const p = document.getElementById(x.getAttribute('aria-controls')); if (p) p.hidden = x !== t })
      if (focus) t.focus()
      if (push && list.dataset.hash !== undefined) history.replaceState(null, '', '#' + t.getAttribute('aria-controls'))
    }
    tabs.forEach((t) => {
      t.addEventListener('click', () => select(t, false, true))
      t.addEventListener('keydown', (e) => {
        const i = tabs.indexOf(t)
        if (e.key === 'ArrowRight') { e.preventDefault(); select(tabs[(i + 1) % tabs.length], true, true) }
        if (e.key === 'ArrowLeft') { e.preventDefault(); select(tabs[(i - 1 + tabs.length) % tabs.length], true, true) }
        if (e.key === 'Home') { e.preventDefault(); select(tabs[0], true, true) }
        if (e.key === 'End') { e.preventDefault(); select(tabs[tabs.length - 1], true, true) }
      })
    })
    const fromHash = list.dataset.hash !== undefined && tabs.find((t) => '#' + t.getAttribute('aria-controls') === location.hash)
    select(fromHash || tabs.find((t) => t.getAttribute('aria-selected') === 'true') || tabs[0])
    if (fromHash) list.closest('section')?.scrollIntoView()
  })
}

// ---------------------------------------------------------------------------
// Landing: replay an attach in the hero terminal
function wireTerm() {
  const body = document.getElementById('term-body'); if (!body) return
  const items = [...body.children]
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches
  const status = document.getElementById('term-status')
  const show = () => { items.forEach((el) => el.classList.add('on')); if (status) status.textContent = 'Attached · live' }
  if (reduce) return show()
  let i = 0
  const step = () => {
    if (i >= items.length) { if (status) status.textContent = 'Attached · live'; return }
    items[i].classList.add('on')
    const wait = items[i].classList.contains('term-sep') ? 420 : items[i].dataset.live ? 900 : 130
    i++
    setTimeout(step, wait)
  }
  setTimeout(step, 400)
  document.getElementById('term-replay')?.addEventListener('click', () => {
    items.forEach((el) => el.classList.remove('on'))
    if (status) status.textContent = 'Replaying backlog…'
    i = 0; setTimeout(step, 200)
  })
}

// ---------------------------------------------------------------------------
// Docs: section filter + scroll spy
function wireDocs() {
  const prose = document.getElementById('prose'); if (!prose) return
  const sections = [...prose.querySelectorAll(':scope > section')]
  const links = [...document.querySelectorAll('.docs-link')]
  const toc = document.getElementById('docs-toc-list')
  const q = document.getElementById('docs-q')
  const empty = document.getElementById('docs-empty')
  const jump = document.getElementById('docs-jump')

  q?.addEventListener('input', () => {
    const term = q.value.trim().toLowerCase()
    let shown = 0
    sections.forEach((s) => {
      const hit = !term || s.innerText.toLowerCase().includes(term)
      s.classList.toggle('docs-hidden', !hit)
      links.find((l) => l.hash === '#' + s.id)?.classList.toggle('docs-hidden', !hit)
      if (hit) shown++
    })
    document.querySelectorAll('.docs-group').forEach((g) => { g.classList.toggle('docs-hidden', !g.querySelector('.docs-link:not(.docs-hidden)')) })
    empty.hidden = shown > 0
    document.getElementById('docs-noresult').hidden = shown > 0
  })
  document.getElementById('docs-clear')?.addEventListener('click', () => { q.value = ''; q.dispatchEvent(new Event('input')); q.focus() })
  jump?.addEventListener('change', () => { location.hash = jump.value })

  const fillToc = (sec) => {
    if (!toc) return
    const hs = [...sec.querySelectorAll('h3[id]')]
    toc.innerHTML = hs.length ? hs.map((h) => `<a href="#${h.id}">${h.textContent}</a>`).join('') : `<span class="faint" style="font-size:var(--fs-xs)">No subsections</span>`
  }
  let current
  const spy = new IntersectionObserver((entries) => {
    entries.forEach((en) => {
      if (!en.isIntersecting || current === en.target) return
      current = en.target
      links.forEach((l) => l.setAttribute('aria-current', String(l.hash === '#' + current.id)))
      if (jump) jump.value = '#' + current.id
      fillToc(current)
    })
  }, { rootMargin: '-20% 0px -70% 0px' })
  sections.forEach((s) => spy.observe(s))
  fillToc(sections[0])
}

// ---------------------------------------------------------------------------
// Telemetry layer — fibre strands after the telemetry references.
const NS = 'http://www.w3.org/2000/svg'
const REDUCE = matchMedia('(prefers-reduced-motion: reduce)').matches
const SMALL = () => matchMedia('(max-width: 767px)').matches

function el(tag, attrs = {}, parent) {
  const e = document.createElementNS(NS, tag)
  for (const k in attrs) e.setAttribute(k, attrs[k])
  parent?.appendChild(e)
  return e
}
function linearGrad(defs, id, x1, y1, x2, y2, stops) {
  const g = el('linearGradient', { id, gradientUnits: 'userSpaceOnUse', x1, y1, x2, y2 }, defs)
  stops.forEach(([o, c, a = 1]) => el('stop', { offset: o, 'stop-color': c, 'stop-opacity': a }, g))
  return `url(#${id})`
}
// draw-in: dash each path to its length, then release with a small stagger
function drawIn(paths) {
  if (REDUCE) return
  paths.forEach((p, i) => {
    if (p.getAttribute('stroke-dasharray')) return
    const len = p.getTotalLength(); if (!len) return
    p.style.strokeDasharray = len; p.style.strokeDashoffset = len
    p.classList.add('draw')
    setTimeout(() => { p.style.strokeDashoffset = 0 }, 40 + Math.min(i * 6, 360))
  })
}
// a bundle of strands from a to b that swell apart mid-way and converge at both ends
// deterministic noise so strands don't reshuffle on every resize
function rng(seed) { let s = seed >>> 0; return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 4294967296) }
// a fibre bundle from a to b: strands that swell apart mid-way and converge at both ends
function bundle(svg, a, b, { n = 14, spread = 60, stroke, seed = 1, core = true, op = [0.12, 0.45], net = '' }) {
  const r = rng(seed); const out = []
  const dx = b.x - a.x
  for (let i = 0; i < n; i++) {
    const t = n === 1 ? 0 : i / (n - 1) - 0.5
    const off = t * spread * (0.6 + r() * 0.8)
    const c1 = { x: a.x + dx * (0.32 + r() * 0.1), y: a.y + off }
    const c2 = { x: a.x + dx * (0.62 + r() * 0.1), y: b.y + off * 0.45 }
    out.push(el('path', { class: 's', d: `M${a.x},${a.y} C${c1.x},${c1.y} ${c2.x},${c2.y} ${b.x},${b.y}`, stroke, opacity: (op[0] + r() * (op[1] - op[0])).toFixed(2), 'data-net': net }, svg))
  }
  if (core) out.push(el('path', { class: 'core', d: `M${a.x},${a.y} C${a.x + dx * 0.4},${a.y} ${a.x + dx * 0.6},${b.y} ${b.x},${b.y}`, stroke, opacity: 0.85, 'data-net': net }, svg))
  return out
}
// layout-space anchor (offset chain, not getBoundingClientRect) so a 3D-tilted
// stage doesn't skew the endpoints — the SVG lives in the same tilted space
function anchorOf(node, container, side) {
  let x = 0, y = 0, n = node
  while (n && n !== container) { x += n.offsetLeft; y += n.offsetTop; n = n.offsetParent }
  const w = node.offsetWidth, h = node.offsetHeight
  return { x: side === 'right' ? x + w : side === 'left' ? x : x + w / 2, y: y + h / 2 }
}

// Hero map: clients → evrd → networks
function wireMap() {
  const map = document.getElementById('map'); if (!map) return
  const svg = map.querySelector('.map-strands')
  const draw = () => {
    svg.replaceChildren()
    if (SMALL()) return
    const box = map
    const W = map.offsetWidth, H = map.offsetHeight
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`)
    const defs = el('defs', {}, svg)
    const hub = map.querySelector('.core-ring')
    const hl = anchorOf(hub, box, 'left'), hr = anchorOf(hub, box, 'right')
    const all = []
    map.querySelectorAll('.map-col.left .downlink').forEach((d, i) => {
      const a = anchorOf(d.querySelector('.anchor-tick'), box, 'right')
      const off = d.classList.contains('off')
      const stroke = linearGrad(defs, 'gl' + i, a.x, 0, hl.x, 0, off ? [[0, 'oklch(66% 0.016 215)', 0.6], [1, 'oklch(66% 0.016 215)', 0.1]] : [[0, 'oklch(90% 0.03 215)'], [0.55, 'oklch(80% 0.10 205)'], [1, 'oklch(66% 0.09 210)']])
      all.push(...bundle(svg, a, hl, { n: off ? 4 : 16, spread: off ? 20 : 70, stroke, seed: 11 + i, op: off ? [0.08, 0.2] : [0.12, 0.5], core: !off }))
      el('circle', { cx: a.x, cy: a.y, r: 2.5, fill: off ? 'var(--faint)' : 'var(--ice)' }, svg)
    })
    map.querySelectorAll('.map-col.right .downlink').forEach((d, i) => {
      const b = anchorOf(d.querySelector('.anchor-tick'), box, 'left')
      const warn = d.classList.contains('warn'), off = d.classList.contains('off')
      const end = warn ? 'oklch(70% 0.16 45)' : 'oklch(80% 0.10 205)'
      const stroke = linearGrad(defs, 'gr' + i, hr.x, 0, b.x, 0, off ? [[0, 'oklch(66% 0.016 215)', 0.2], [1, 'oklch(66% 0.016 215)', 0.4]] : [[0, 'oklch(80% 0.10 205)'], [0.45, 'oklch(90% 0.03 215)'], [1, end]])
      all.push(...bundle(svg, hr, b, { n: off ? 3 : warn ? 10 : 16, spread: off ? 14 : 64, stroke, seed: 31 + i, op: off ? [0.06, 0.16] : [0.12, 0.5], core: !off }))
      el('circle', { cx: b.x, cy: b.y, r: 2.5, fill: off ? 'var(--faint)' : end }, svg)
    })
    drawIn(all)
  }
  let t; const redraw = () => { clearTimeout(t); t = setTimeout(draw, 120) }
  addEventListener('resize', redraw)
  document.fonts?.ready.then(draw); draw()
}

// Topology: network cards → channel rows, with filters and focus highlight
function wireTopo() {
  const topo = document.getElementById('topo'); if (!topo) return
  const svg = topo.querySelector('.topo-links')
  const nets = [...topo.querySelectorAll('.net-node')]
  const chans = [...topo.querySelectorAll('.chan-node')]
  const empty = document.getElementById('topo-empty')
  let filter = 'all', focusNet = null
  const visible = (c) => (filter === 'all' || (filter === 'unread' && +c.dataset.unread > 0) || (filter === 'mention' && +c.dataset.mention > 0)) && (!focusNet || c.dataset.net === focusNet)
  const draw = () => {
    svg.replaceChildren()
    chans.forEach((c) => { c.hidden = !visible(c) })
    empty.hidden = chans.some((c) => !c.hidden)
    if (SMALL()) return
    const box = topo
    svg.setAttribute('viewBox', `0 0 ${topo.offsetWidth} ${topo.offsetHeight}`)
    const defs = el('defs', {}, svg)
    chans.filter((c) => !c.hidden).forEach((c, i) => {
      const n = topo.querySelector(`.net-node[data-net="${c.dataset.net}"]`)
      const a = anchorOf(n, box, 'right'), b = anchorOf(c, box, 'left')
      const col = n.dataset.color
      const stroke = linearGrad(defs, 'tl' + i, a.x, 0, b.x, 0, [[0, col], [0.6, 'oklch(90% 0.03 215)', 0.8], [1, col]])
      const hot = +c.dataset.mention > 0
      bundle(svg, a, b, { n: hot ? 6 : 3, spread: hot ? 22 : 10, stroke, seed: 7 + i, op: hot ? [0.25, 0.7] : [0.18, 0.45], net: c.dataset.net })
      el('circle', { cx: b.x, cy: b.y, r: hot ? 3 : 2, fill: hot ? 'var(--bad)' : col, 'data-net': c.dataset.net }, svg)
    })
  }
  document.querySelectorAll('#topo-filter .tab').forEach((b) => b.addEventListener('click', () => {
    filter = b.dataset.f
    document.querySelectorAll('#topo-filter .tab').forEach((x) => x.setAttribute('aria-pressed', String(x === b)))
    draw()
  }))
  nets.forEach((n) => n.addEventListener('click', () => {
    focusNet = focusNet === n.dataset.net ? null : n.dataset.net
    nets.forEach((x) => x.setAttribute('aria-pressed', String(x.dataset.net === focusNet)))
    document.getElementById('topo-scope').textContent = focusNet ? `Showing ${n.dataset.name} only — select it again to show every network.` : 'Select a network to show only its channels.'
    draw()
  }))
  // hovering or focusing a network brightens its links and dims the rest
  const hl = (id) => svg.querySelectorAll('[data-net]').forEach((p) => {
    p.classList.toggle('hl', !!id && p.dataset.net === id)
    p.classList.toggle('dim', !!id && p.dataset.net !== id)
  })
  nets.forEach((n) => {
    n.addEventListener('mouseenter', () => hl(n.dataset.net)); n.addEventListener('mouseleave', () => hl(null))
    n.addEventListener('focus', () => hl(n.dataset.net)); n.addEventListener('blur', () => hl(null))
  })
  // counts on the filter buttons
  const cnt = { all: chans.length, unread: chans.filter((c) => +c.dataset.unread > 0).length, mention: chans.filter((c) => +c.dataset.mention > 0).length }
  document.querySelectorAll('#topo-filter [data-n]').forEach((s) => { s.textContent = cnt[s.dataset.n] })
  let t; addEventListener('resize', () => { clearTimeout(t); t = setTimeout(draw, 120) })
  document.fonts?.ready.then(draw); draw()
}

// Traffic: lines per hour as layered strands (sample data)
function wireTraffic() {
  const plot = document.getElementById('traffic-plot'); if (!plot) return
  const W = 1000, H = 300, MAX = 1600
  const svg = el('svg', { viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: 'none', 'aria-hidden': 'true' })
  plot.prepend(svg)
  const defs = el('defs', {}, svg)
  // 24 hourly samples, 00:00 → 23:00, shaped to match the console prototype's story
  const SERIES = [
    { id: 'libera', c: ['oklch(70% 0.16 45)', 'oklch(90% 0.03 215)', 'oklch(80% 0.10 205)'], d: [380, 300, 240, 210, 190, 200, 260, 420, 640, 820, 960, 1040, 1120, 1210, 1330, 1400, 1360, 1240, 1080, 920, 760, 620, 520, 440] },
    { id: 'oftc', c: ['oklch(80% 0.07 240)', 'oklch(90% 0.03 215)', 'oklch(80% 0.07 240)'], d: [220, 200, 170, 150, 140, 150, 190, 280, 380, 470, 520, 560, 600, 640, 660, 700, 690, 650, 600, 540, 470, 400, 330, 270] },
    { id: 'hackint', c: ['oklch(82% 0.13 80)', 'oklch(70% 0.16 45)', 'oklch(68% 0.18 27)'], d: [160, 150, 130, 40, 0, 0, 0, 90, 180, 240, 300, 320, 340, 360, 380, 400, 390, 360, 330, 300, 260, 230, 200, 180] },
  ]
  const x = (i) => (i / 23) * W, y = (v) => H - (v / MAX) * H
  const path = (pts) => pts.map((p, i) => {
    if (!i) return `M${p.x},${p.y}`
    const q = pts[i - 1], mx = (q.x + p.x) / 2
    return `C${mx},${q.y} ${mx},${p.y} ${p.x},${p.y}`
  }).join('')
  const all = []
  SERIES.forEach((s, si) => {
    const stroke = linearGrad(defs, 'tr' + si, 0, 0, W, 0, [[0, s.c[0]], [0.5, s.c[1]], [1, s.c[2]]])
    const r = rng(101 + si)
    const line = s.d.map((v, i) => ({ x: x(i), y: y(v) }))
    el('path', { class: 'bloom', d: path(line), stroke }, svg)
    for (let k = 0; k < (SMALL() ? 4 : 9); k++) {
      const amp = 10 + r() * 34
      const pts = s.d.map((v, i) => ({ x: x(i), y: y(Math.max(0, v + (r() - 0.5) * amp * (v ? 4 : 0.5))) }))
      all.push(el('path', { class: 'strand', d: path(pts), stroke, opacity: (0.25 + r() * 0.45).toFixed(2) }, svg))
    }
    all.push(el('path', { class: 'core', d: path(line), stroke: s.id === 'libera' ? 'var(--ice)' : stroke, opacity: 0.9 }, svg))
  })
  drawIn(all)
}

// Ribbons on docs / download headers
function wireRibbons() {
  document.querySelectorAll('svg.ribbon').forEach((svg, ri) => {
    const draw = () => {
      svg.replaceChildren()
      const W = svg.clientWidth || 1000, H = 72
      svg.setAttribute('viewBox', `0 0 ${W} ${H}`)
      const defs = el('defs', {}, svg)
      const stroke = linearGrad(defs, 'rb' + ri, 0, 0, W, 0, [[0, 'oklch(70% 0.16 45)', 0.2], [0.35, 'oklch(90% 0.03 215)'], [0.7, 'oklch(80% 0.10 205)'], [1, 'oklch(80% 0.10 205)', 0.1]])
      const r = rng(55 + ri); const out = []
      const n = SMALL() ? 8 : 18
      for (let i = 0; i < n; i++) {
        const y0 = H * 0.5 + (r() - 0.5) * 12, y1 = H * (0.2 + r() * 0.6), y2 = H * (0.2 + r() * 0.6)
        out.push(el('path', { d: `M0,${y0} C${W * 0.25},${y1} ${W * 0.55},${y2} ${W},${H * 0.5 + (r() - 0.5) * 30}`, stroke, 'stroke-width': 0.8, opacity: (0.15 + r() * 0.45).toFixed(2) }, svg))
      }
      drawIn(out)
    }
    let t; addEventListener('resize', () => { clearTimeout(t); t = setTimeout(draw, 150) })
    draw()
  })
}

// ---------------------------------------------------------------------------
// Diagrams — any .diagram with nodes marked data-node="id" and links in
// data-links="a>b:kind, c>d:kind" (or data-links-<step> per stepper step).
// Kinds: ok (cyan fibre), data (database traffic), warn (ember), bad (failed,
// dashed), int (node-to-node, dashed), off (idle). Links run horizontally or
// vertically depending on where the two nodes sit.
const KINDS = {
  ok:   { n: 12, spread: 36, op: [0.12, 0.5], stops: [[0, 'oklch(90% 0.03 215)'], [0.5, 'oklch(80% 0.10 205)'], [1, 'oklch(66% 0.09 210)']] },
  data: { n: 8, spread: 22, op: [0.12, 0.45], stops: [[0, 'oklch(80% 0.07 240)'], [0.5, 'oklch(90% 0.03 215)'], [1, 'oklch(80% 0.07 240)']] },
  warn: { n: 9, spread: 28, op: [0.14, 0.55], stops: [[0, 'oklch(82% 0.13 80)'], [0.5, 'oklch(70% 0.16 45)'], [1, 'oklch(70% 0.16 45)']] },
  bad:  { n: 1, spread: 0, op: [0.75, 0.75], dash: '3 6', stops: [[0, 'oklch(68% 0.18 27)'], [1, 'oklch(68% 0.18 27)', 0.5]] },
  int:  { n: 1, spread: 0, op: [0.7, 0.7], dash: '2 5', stops: [[0, 'oklch(90% 0.03 215)'], [1, 'oklch(80% 0.07 240)']] },
  off:  { n: 3, spread: 10, op: [0.06, 0.16], stops: [[0, 'oklch(66% 0.016 215)'], [1, 'oklch(66% 0.016 215)', 0.5]] },
}
function boxOf(node, container) {
  let x = 0, y = 0, n = node
  while (n && n !== container) { x += n.offsetLeft; y += n.offsetTop; n = n.offsetParent }
  return { x, y, w: node.offsetWidth, h: node.offsetHeight }
}
// one fibre bundle in either orientation: strands open mid-way and meet at both ends
function fibre(svg, a, b, { orient = 'h', n, spread, stroke, seed, op, dash, core = true }) {
  const r = rng(seed), out = []
  const P = (along, across) => orient === 'h' ? `${along},${across}` : `${across},${along}`
  const A = orient === 'h' ? [a.x, a.y] : [a.y, a.x], B = orient === 'h' ? [b.x, b.y] : [b.y, b.x]
  const d = B[0] - A[0]
  const dashAttr = dash ? { 'stroke-dasharray': dash } : {}
  for (let i = 0; i < n && n > 1; i++) {
    const t = i / (n - 1) - 0.5, off = t * spread * (0.6 + r() * 0.8)
    out.push(el('path', { class: 's', d: `M${P(A[0], A[1])} C${P(A[0] + d * (0.32 + r() * 0.1), A[1] + off)} ${P(A[0] + d * (0.62 + r() * 0.1), B[1] + off * 0.45)} ${P(B[0], B[1])}`, stroke, opacity: (op[0] + r() * (op[1] - op[0])).toFixed(2), ...dashAttr }, svg))
  }
  if (core) out.push(el('path', { class: 'core', d: `M${P(A[0], A[1])} C${P(A[0] + d * 0.45, A[1])} ${P(A[0] + d * 0.55, B[1])} ${P(B[0], B[1])}`, stroke, opacity: op[1], ...dashAttr }, svg))
  return out
}
function parseLinks(spec) {
  return (spec || '').split(',').map((x) => x.trim()).filter(Boolean).map((x) => {
    const [pair, kind = 'ok'] = x.split(':'); const [from, to] = pair.split('>')
    return { from: from.trim(), to: to.trim(), kind: kind.trim() }
  })
}
function wireDiagrams() {
  document.querySelectorAll('.diagram').forEach((dg, di) => {
    let svg = dg.querySelector(':scope > .dg-links')
    if (!svg) { svg = el('svg', { class: 'dg-links', 'aria-hidden': 'true' }); dg.prepend(svg) }
    const node = (id) => dg.querySelector(`[data-node="${id}"]`)
    let focus = null
    const applyFocus = () => {
      svg.querySelectorAll('path, circle').forEach((p) => {
        const on = !!focus && (p.dataset.from === focus || p.dataset.to === focus)
        p.classList.toggle('hl', on); p.classList.toggle('dim', !!focus && !on)
      })
      dg.querySelectorAll('[data-node]').forEach((n) => n.classList.toggle('is-hl', n.dataset.node === focus))
    }
    const draw = () => {
      svg.replaceChildren()
      if (SMALL() || !dg.offsetWidth) return
      svg.setAttribute('viewBox', `0 0 ${dg.offsetWidth} ${dg.offsetHeight}`)
      const defs = el('defs', {}, svg)
      const step = dg.dataset.step, mode = dg.dataset.mode
      const spec = (step != null && mode && dg.getAttribute(`data-links-${mode}-${step}`)) || (step != null && dg.getAttribute('data-links-' + step)) || dg.dataset.links || ''
      const all = []
      parseLinks(spec).forEach((l, i) => {
        const na = node(l.from), nb = node(l.to); if (!na || !nb) return
        const ra = boxOf(na, dg), rb = boxOf(nb, dg)
        const ca = { x: ra.x + ra.w / 2, y: ra.y + ra.h / 2 }, cb = { x: rb.x + rb.w / 2, y: rb.y + rb.h / 2 }
        const apartX = ra.x + ra.w <= rb.x || rb.x + rb.w <= ra.x
        const horiz = apartX && Math.abs(cb.x - ca.x) > Math.abs(cb.y - ca.y) * 0.6
        let a, b, orient
        if (horiz) { orient = 'h'; const right = cb.x > ca.x; a = { x: right ? ra.x + ra.w : ra.x, y: ca.y }; b = { x: right ? rb.x : rb.x + rb.w, y: cb.y } }
        else { orient = 'v'; const down = cb.y > ca.y; a = { x: ca.x, y: down ? ra.y + ra.h : ra.y }; b = { x: cb.x, y: down ? rb.y : rb.y + rb.h } }
        const k = KINDS[l.kind] || KINDS.ok
        const stroke = linearGrad(defs, `dg${di}-${i}`, a.x, a.y, b.x, b.y, k.stops)
        const paths = fibre(svg, a, b, { orient, n: SMALL() ? Math.min(k.n, 4) : k.n, spread: k.spread, stroke, seed: 17 + di * 31 + i * 7, op: k.op, dash: k.dash })
        paths.forEach((p) => { p.dataset.from = l.from; p.dataset.to = l.to; p.classList.add('k-' + l.kind) })
        all.push(...paths)
        ;[a, b].forEach((pt, j) => { const c = el('circle', { cx: pt.x, cy: pt.y, r: 2.5, class: 'k-' + l.kind }, svg); c.dataset.from = l.from; c.dataset.to = l.to; c.style.fill = k.stops[j ? k.stops.length - 1 : 0][1] })
      })
      drawIn(all)
      applyFocus()
    }
    let t; new ResizeObserver(() => { clearTimeout(t); t = setTimeout(draw, 80) }).observe(dg)
    dg.addEventListener('redraw', draw)
    const setFocus = (e) => { const n = e.target.closest?.('[data-node]'); focus = n && dg.contains(n) ? n.dataset.node : null; applyFocus() }
    dg.addEventListener('pointerover', setFocus)
    dg.addEventListener('pointerleave', () => { focus = null; applyFocus() })
    dg.addEventListener('focusin', setFocus)
    dg.addEventListener('focusout', () => { focus = null; applyFocus() })
    document.fonts?.ready.then(draw)
  })
}

// Steppers — [data-stepper] wraps a .diagram whose nodes carry data-states="s0,s1,…"
// (data-states-<mode> overrides per mode) and captions [data-cap="i"] (optionally data-mode).
const STATE_LABEL = { owner: 'Owner', front: 'Front door', down: 'Down', drain: 'Draining', taking: 'Taking over', reconnect: 'Reconnecting', live: 'Live', idle: 'Standby', lookup: 'Lease lookup', retry: 'Retrying', connected: 'Connected' }
function wireSteppers() {
  document.querySelectorAll('[data-stepper]').forEach((st) => {
    const dg = st.querySelector('.diagram')
    const total = +st.dataset.steps
    const caps = [...st.querySelectorAll('[data-cap]')]
    const dots = [...st.querySelectorAll('.step-dot')]
    const play = st.querySelector('[data-play]')
    const count = st.querySelector('.step-count')
    let i = 0, timer = null
    const mode = () => st.dataset.mode || ''
    const set = (k) => {
      i = (k + total) % total
      dg.dataset.step = i
      if (mode()) dg.dataset.mode = mode(); else delete dg.dataset.mode
      dg.querySelectorAll('[data-states]').forEach((n) => {
        const list = (n.getAttribute('data-states-' + mode()) || n.dataset.states).split(',').map((x) => x.trim())
        const state = list[Math.min(i, list.length - 1)] || ''
        n.dataset.state = state
        const chip = n.querySelector('.st-chip')
        if (chip) { chip.textContent = STATE_LABEL[state] || ''; chip.hidden = !STATE_LABEL[state] }
      })
      caps.forEach((c) => { c.hidden = +c.dataset.cap !== i || (!!c.dataset.mode && c.dataset.mode !== mode()) })
      dots.forEach((d, j) => d.setAttribute('aria-current', String(j === i)))
      if (count) count.textContent = `Step ${i + 1} of ${total}`
      dg.dispatchEvent(new Event('redraw'))
    }
    const stop = () => { clearInterval(timer); timer = null; if (play) { play.innerHTML = icon('play') + 'Play'; play.setAttribute('aria-pressed', 'false') } }
    st.querySelector('[data-prev]')?.addEventListener('click', () => { stop(); set(i - 1) })
    st.querySelector('[data-next]')?.addEventListener('click', () => { stop(); set(i + 1) })
    dots.forEach((d, j) => d.addEventListener('click', () => { stop(); set(j) }))
    play?.addEventListener('click', () => {
      if (timer) return stop()
      play.innerHTML = icon('pause') + 'Pause'; play.setAttribute('aria-pressed', 'true')
      if (i === total - 1) set(0)
      timer = setInterval(() => { if (i === total - 1) return stop(); set(i + 1) }, 2400)
    })
    st.querySelectorAll('[data-mode-btn]').forEach((b) => b.addEventListener('click', () => {
      st.dataset.mode = b.dataset.modeBtn
      st.querySelectorAll('[data-mode-btn]').forEach((x) => x.setAttribute('aria-pressed', String(x === b)))
      stop(); set(0)
    }))
    set(0)
  })
}

// ---------------------------------------------------------------------------
shell()
hydrateIcons()
wireCopy()
wireTabs()
wireTerm()
wireDocs()
wireMap()
wireTopo()
wireTraffic()
wireRibbons()
wireDiagrams()
wireSteppers()
window.EVRD_SITE = { icon, toast }
