<script>
  // Sidebar + top bar around every signed-in screen (mobile: the sidebar is a
  // drawer; wider, it can be collapsed to its icons, remembered per browser).
  // The top bar searches networks and channels, jumps to mentions,
  // and shows recent activity.
  import { onMount, onDestroy } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from './Icon.svelte'
  import { api } from '$lib/api.js'
  import { session, logout, refreshNets, refreshMe, mentionsTotal } from '$lib/session.svelte.js'
  import { ratio, clock, ago } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  let { flush = false, children } = $props()

  const me = $derived(session.me)
  const isAdmin = $derived(me?.role === 'admin')
  const path = $derived(page.url.pathname)
  const mentions = $derived(mentionsTotal())
  const up = $derived(session.nets.filter((n) => n.status === 'connected').length)

  const NAV = $derived([
    { group: t('shell.groupBouncer'), items: [
      { href: '/', label: t('shell.navOverview'), ic: 'grid' },
      { href: '/networks', label: t('shell.navNetworks'), ic: 'globe', count: session.nets.length, cls: 'calm' },
      { href: '/chat', label: t('shell.navChat'), ic: 'message', count: mentions || null, cls: 'mention' },
      { href: '/clients', label: t('shell.navClients'), ic: 'plug' },
    ] },
    ...(isAdmin ? [{ group: t('shell.groupAdmin'), items: [
      { href: '/admin', label: t('shell.navOverview'), ic: 'chart' },
      { href: '/admin/users', label: t('shell.navUsers'), ic: 'users' },
      { href: '/admin/metrics', label: t('shell.navMetrics'), ic: 'activity' },
      { href: '/admin/drops', label: t('shell.navDrops'), ic: 'monitor' },
      { href: '/admin/settings', label: t('shell.navSettings'), ic: 'sliders' },
    ] }] : []),
    { group: t('shell.groupAccount'), items: [{ href: '/account', label: t('shell.navAccount'), ic: 'user' }] },
  ])
  const cur = (href) => (href === '/' || href === '/admin' ? path === href : path === href || path.startsWith(href + '/'))

  // mobile drawer
  let open = $state(false)
  let narrow = $state(false)
  let burger = $state()
  $effect(() => {
    const mq = matchMedia('(max-width: 959px)')
    const sync = () => { narrow = mq.matches; if (!mq.matches) open = false }
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  })
  $effect(() => { path; open = false })
  function close() { open = false; burger?.focus() }

  // collapsed to icons (not on a phone, where it's a drawer anyway)
  const COLLAPSED = 'evrd.navCollapsed'
  let collapsed = $state(false)
  const slim = $derived(collapsed && !narrow)
  onMount(() => { try { collapsed = localStorage.getItem(COLLAPSED) === '1' } catch {} })
  function toggleCollapsed() {
    collapsed = !collapsed
    try { localStorage.setItem(COLLAPSED, collapsed ? '1' : '0') } catch {}
  }

  // keep the counts fresh
  let timer
  onMount(() => { timer = setInterval(() => { refreshNets(); refreshMe().catch(() => {}) }, 15000) })
  onDestroy(() => { clearInterval(timer); clearTimeout(closeT) })

  // ---- global search: networks and channels
  let q = $state('')
  let searchIn = $state()
  let sel = $state(0)
  let searchOpen = $state(false)
  // Closing waits a moment after blur so a click on a result still lands;
  // focusing again (or typing) cancels it, or picking a result and typing
  // straight away would close the new results under the cursor.
  let closeT
  const openSearch = () => { clearTimeout(closeT); searchOpen = true }
  const results = $derived.by(() => {
    const s = q.trim().toLowerCase()
    if (!s) return []
    const out = []
    for (const n of session.nets) {
      if ([n.name, n.host, n.currentNick || n.nick].join(' ').toLowerCase().includes(s)) out.push({ href: `/networks#net-${n.id}`, ic: 'globe', name: n.name, sub: n.host })
      for (const c of n.channels || []) if (c.toLowerCase().includes(s)) out.push({ href: `/chat?net=${n.id}&buf=${encodeURIComponent(c)}`, ic: 'hash', name: c, sub: n.name })
    }
    return out.slice(0, 8)
  })
  function onSearchKey(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(sel + 1, results.length - 1) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(sel - 1, 0) }
    else if (e.key === 'Enter') {
      e.preventDefault()
      const hit = results[sel]
      if (hit) goto(hit.href)
      else if (q.trim()) goto('/networks?q=' + encodeURIComponent(q.trim()))
      q = ''; searchOpen = false; searchIn?.blur()
    } else if (e.key === 'Escape') { q = ''; searchIn?.blur() }
  }
  $effect(() => { q; sel = 0 })

  // ⌘K / Ctrl+K: the page's own search if it has one, else the global one
  function onKey(e) {
    if (e.key === 'Escape' && open) close()
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      ;(document.getElementById('page-search') || searchIn)?.focus()
    }
  }

  // ---- mentions + activity
  function mentionsHref() {
    for (const n of session.nets) if (n.mentions) return `/chat?net=${n.id}&mentions=1`
    return '/chat'
  }
  let actOpen = $state(false)
  let events = $state(null)
  async function toggleActivity() {
    actOpen = !actOpen
    if (actOpen) { try { events = await api.events(15) } catch { events = [] } }
  }
</script>

<svelte:window onkeydown={onKey} onclick={(e) => { if (actOpen && !e.target.closest('.pop-anchor')) actOpen = false }} />

<div class="app" class:collapsed={slim}>
  <a class="skip" href="#main">{t('shell.skip')}</a>
  <aside class="sidebar" class:open id="sidebar" aria-label={t('shell.navigation')} inert={narrow && !open}>
    <a class="brand" href="/" title={slim ? 'evrd' : undefined}><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>{t('shell.tagline')}</span></span></a>
    {#each NAV as g}
      <nav class="nav-group" aria-label={g.group}>
        <span class="caps">{g.group}</span>
        {#each g.items as n}
          <a class="nav-link" href={n.href} aria-current={cur(n.href) ? 'page' : undefined} title={slim ? n.label : undefined}><Icon name={n.ic} /><span>{n.label}</span>{#if n.count}<span class="count {n.cls}">{n.count}</span>{/if}</a>
        {/each}
      </nav>
    {/each}
    <div class="side-foot">
      {#if me}
        <div class="health-card" data-testid="side-card">
          <div class="od-row" style="justify-content:space-between">
            {#if isAdmin}<span class="chip ok"><Icon name="crown" />{t('shell.admin')}</span>{:else}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('shell.active')}</span>{/if}
            <span class="caps">{t('shell.bouncer')}</span>
          </div>
          <div class="od-stack" style="--od-gap:2px">
            <span class="who od-truncate">{me.username}@{session.config?.ircHost}</span>
            <span class="sub">{t('shell.netsUp', { up, total: session.nets.length, attached: me.usage.attached })}</span>
            {#if session.config?.version}<span class="sub version" data-testid="evrd-version">{t('shell.version', { version: session.config.version })}</span>{/if}
          </div>
          {#if me.limits.maxNetworks}
            <span class="ubar" role="img" aria-label={t('shell.netsUsed', { used: me.usage.networks, max: me.limits.maxNetworks })}><span style="transform:scaleX({ratio(me.usage.networks, me.limits.maxNetworks)})"></span></span>
          {/if}
        </div>
      {/if}
      <button class="btn btn-ghost side-btn" type="button" onclick={logout} title={slim ? t('shell.signOut') : undefined}><Icon name="logout" /><span>{t('shell.signOut')}</span></button>
    </div>
  </aside>
  <div class="scrim" class:show={open} onclick={close} aria-hidden="true"></div>

  <div class="workspace">
    <header class="topbar">
      <button class="icon-btn burger" type="button" aria-label={t('shell.openNav')} aria-controls="sidebar" aria-expanded={open} bind:this={burger} onclick={() => (open = true)}><Icon name="menu" /></button>
      <button class="icon-btn side-toggle" type="button" aria-label={slim ? t('shell.expandNav') : t('shell.collapseNav')} title={slim ? t('shell.expandNav') : t('shell.collapseNav')} aria-controls="sidebar" onclick={toggleCollapsed}><Icon name="panel" /></button>
      <div class="search-wrap hide-sm">
        <label class="search" role="search">
          <Icon name="search" /><span class="sr-only">{t('shell.search')}</span>
          <input id="global-search" type="search" role="combobox" placeholder={t('shell.searchPlaceholder')} autocomplete="off" bind:value={q} bind:this={searchIn}
            onfocus={openSearch} oninput={openSearch} onblur={() => { closeT = setTimeout(() => (searchOpen = false), 150) }} onkeydown={onSearchKey}
            aria-controls="search-results" aria-expanded={searchOpen && results.length > 0} />
          <span class="kbd" aria-hidden="true">⌘ K</span>
        </label>
        {#if searchOpen && results.length}
          <div class="search-results" id="search-results" role="listbox">
            {#each results as r, i}
              <a href={r.href} role="option" aria-selected={i === sel} onclick={() => { q = '' }}><Icon name={r.ic} class="i-sm" /><span class="od-truncate">{r.name}</span><span class="sub od-truncate">{r.sub}</span></a>
            {/each}
          </div>
        {/if}
      </div>
      <div class="top-actions">
        <a class="icon-btn" href={mentionsHref()} aria-label={mentions ? t('shell.mentionsUnread', { count: mentions }) : t('shell.mentionsNone')}><Icon name="at" />{#if mentions}<span class="pip" aria-hidden="true"></span>{/if}</a>
        <div class="pop-anchor">
          <button class="icon-btn" type="button" aria-label={t('shell.activity')} aria-expanded={actOpen} onclick={toggleActivity}><Icon name="bell" /></button>
          {#if actOpen}
            <div class="popover" role="dialog" aria-label={t('shell.recentActivity')}>
              <span class="caps">{t('shell.recentActivity')}</span>
              {#if !events}
                <div class="skel"></div>
              {:else if !events.length}
                <p class="desc">{t('shell.activityEmpty')}</p>
              {:else}
                <div class="event-list">
                  {#each events as e}
                    <div class="event"><time title={ago(e.at)}>{clock(e.at)}</time><div class="od-field"><span>{e.title}</span><span>{e.detail}</span></div></div>
                  {/each}
                </div>
              {/if}
            </div>
          {/if}
        </div>
        <a class="user-pill" href="/account" aria-label={t('shell.accountOf', { name: me?.username })}>
          <span class="avatar" aria-hidden="true">{me?.username?.[0]?.toUpperCase()}</span>
          <span class="od-stat hide-sm"><span>{me?.username}</span><span>{isAdmin ? t('shell.roleAdmin') : t('shell.roleUser')}</span></span>
        </a>
      </div>
    </header>
    <main class="main" class:flush id="main" tabindex="-1">
      <div class="main-inner">
        {@render children()}
      </div>
    </main>
  </div>
</div>
