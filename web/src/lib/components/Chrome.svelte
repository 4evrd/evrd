<script>
  // Sidebar + top bar around every signed-in screen (mobile: sidebar is a drawer).
  import { page } from '$app/state'
  import { beforeNavigate, afterNavigate } from '$app/navigation'
  import Icon from './Icon.svelte'
  import { session, logout } from '$lib/session.svelte.js'
  import { limit } from '$lib/format.js'

  let { crumb = '', children } = $props()

  const NAV = [
    { href: '/', label: 'Overview', ic: 'grid' },
    { href: '/networks', label: 'Networks', ic: 'globe' },
    { href: '/clients', label: 'IRC clients', ic: 'plug' },
  ]
  const ADMIN = [
    { href: '/admin', label: 'Users', ic: 'users' },
  ]
  const me = $derived(session.me)
  const isAdmin = $derived(me?.role === 'admin')
  const path = $derived(page.url.pathname)
  // full-bleed pages (a network's chat) fill the workspace with no page padding
  const flush = $derived(/^\/networks\/\d+/.test(path))
  const cur = (href) => (href === '/' ? path === '/' : path === href || path.startsWith(href + '/'))
  const all = [...NAV, ...ADMIN, { href: '/account', label: 'Account' }]
  const title = $derived(crumb || page.data?.crumb || all.find((n) => cur(n.href))?.label || '')

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

  // The workspace panel scrolls, not the window (see .app in app.css), so do
  // what the browser did for the window: start new pages at the top, and put
  // back the old position on back/forward.
  let ws = $state()
  const scrolls = new Map() // url -> scrollTop
  beforeNavigate(({ from }) => { if (from && ws) scrolls.set(from.url.href, ws.scrollTop) })
  afterNavigate(({ to, type }) => {
    if (!ws || to?.url.hash) return
    ws.scrollTop = type === 'popstate' ? scrolls.get(to?.url.href) ?? 0 : 0
  })
</script>

<svelte:window onkeydown={(e) => { if (e.key === 'Escape' && open) close() }} />

<div class="app" class:flush>
  <aside class="sidebar glass" class:open id="sidebar" aria-label="Navigation" inert={narrow && !open}>
    <a class="brand" href="/">
      <span class="brand-mark" aria-hidden="true">#</span>
      <span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span>
    </a>
    <nav class="nav-group" aria-label="Your bouncer">
      <span class="nav-label">Your bouncer</span>
      {#each NAV as n}
        <a class="nav-link" href={n.href} aria-current={cur(n.href) ? 'page' : undefined}><Icon name={n.ic} /><span>{n.label}</span></a>
      {/each}
    </nav>
    {#if isAdmin}
      <nav class="nav-group" aria-label="Administration">
        <span class="nav-label">Administration</span>
        {#each ADMIN as n}
          <a class="nav-link" href={n.href} aria-current={cur(n.href) ? 'page' : undefined}><Icon name={n.ic} /><span>{n.label}</span></a>
        {/each}
      </nav>
    {/if}
    <nav class="nav-group" aria-label="Account">
      <span class="nav-label">Account</span>
      <a class="nav-link" href="/account" aria-current={cur('/account') ? 'page' : undefined}><Icon name="user" /><span>Account</span></a>
    </nav>
    <div class="side-foot">
      {#if me}
        <div class="shell-card" data-testid="side-card">
          <span>{#if isAdmin}<span class="chip ok"><Icon name="crown" />Admin</span>{:else}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>Active</span>{/if}</span>
          <span class="who">{me.username}@{session.config?.ircHost}</span>
          <span class="host">{me.usage.networks} of {limit(me.limits.maxNetworks)} networks · {me.usage.clients} attached</span>
        </div>
      {/if}
      <button class="btn btn-ghost" type="button" onclick={logout} style="justify-content:flex-start"><Icon name="logout" />Sign out</button>
    </div>
  </aside>
  <div class="scrim" class:show={open} onclick={close} aria-hidden="true"></div>

  <div class="workspace" bind:this={ws}>
    <header class="topbar">
      <div class="crumbs">
        <button class="icon-btn burger" type="button" aria-label="Open navigation" aria-controls="sidebar" aria-expanded={open} bind:this={burger} onclick={() => (open = true)}><Icon name="menu" /></button>
        <a href={path.startsWith('/admin') ? '/admin' : '/'} class="hide-sm"><Icon name={path.startsWith('/admin') ? 'users' : 'grid'} /><span>{path.startsWith('/admin') ? 'Admin' : 'Bouncer'}</span></a>
        <span class="sep hide-sm" aria-hidden="true"><Icon name="chevron" /></span>
        <span aria-current="page">{title}</span>
      </div>
      <div class="top-actions">
        <a class="pill-btn" href="/account"><Icon name="user" /><span>{me?.username}</span></a>
        <button class="icon-btn" type="button" aria-label="Sign out" onclick={logout}><Icon name="logout" /></button>
      </div>
    </header>
    <main class="main" class:flush id="main" tabindex="-1">
      <div class="main-inner">
        {@render children()}
      </div>
    </main>
  </div>
</div>
