<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshNets, refreshMe } from '$lib/session.svelte.js'
  import { goto } from '$app/navigation'
  import { toast } from '$lib/ui.svelte.js'
  import { short, netColor, netGlow, since, clock, limit, wait } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  let nets = $state(null)
  let timer
  async function load() {
    try { nets = await api.networks(); session.nets = nets } catch (e) { if (!nets) toast(e.message, 'bad') }
  }
  onMount(async () => {
    await load()
    timer = setInterval(load, 4000)
    // older deep links: #add and #edit-ID are pages now
    if (location.hash === '#add') return goto('/networks/new', { replaceState: true })
    const m = location.hash.match(/^#(net|edit)-(\d+)$/)
    if (m?.[1] === 'edit') return goto(`/networks/${m[2]}`, { replaceState: true })
    if (m) {
      await tick()
      document.getElementById('net-' + m[2])?.scrollIntoView({ block: 'center' })
    }
  })
  onDestroy(() => clearInterval(timer))

  const me = $derived(session.me)
  const max = $derived(me.limits.maxNetworks)
  const atLimit = $derived(max > 0 && (nets?.length ?? 0) >= max)

  // search + filters
  let filter = $state(page.url.searchParams.get('filter') || 'all')
  let q = $state(page.url.searchParams.get('q') || '')
  // the top bar's search can land here again with a new ?q=
  $effect(() => {
    const sp = page.url.searchParams
    if (sp.has('q')) q = sp.get('q')
    if (sp.has('filter')) filter = sp.get('filter')
  })
  const decorated = $derived((nets || []).map((n, i) => ({ ...n, color: netColor(i), glow: netGlow(i), short: short(n.name) })))
  const match = (n) => (filter === 'all' || (filter === 'notls' ? !n.tls : filter === 'reconnecting' ? n.status === 'reconnecting' || n.status === 'connecting' : n.status === filter)) &&
    (!q.trim() || [n.name, n.host, n.currentNick || n.nick].join(' ').toLowerCase().includes(q.trim().toLowerCase()))
  const list = $derived(decorated.filter(match))
  const counts = $derived({
    all: decorated.length,
    connected: decorated.filter((n) => n.status === 'connected').length,
    reconnecting: decorated.filter((n) => n.status === 'reconnecting' || n.status === 'connecting').length,
    disconnected: decorated.filter((n) => n.status === 'disconnected').length,
    notls: decorated.filter((n) => !n.tls).length,
  })
  const FILTERS = [['all', 'networks.filter.all'], ['connected', 'networks.filter.connected'], ['reconnecting', 'networks.filter.reconnecting'], ['disconnected', 'networks.filter.off'], ['notls', 'networks.filter.notls']]

  // connect / disconnect
  let busy = $state({})
  async function toggle(n, on) {
    busy[n.id] = true
    try {
      if (on) { await api.connect(n.id); toast(t('networks.connecting', { name: n.name })) }
      else { await api.disconnect(n.id); toast(t('networks.disconnected', { name: n.name })) }
      await load()
      refreshMe()
    } catch (e) { toast(e.message, 'bad') } finally { delete busy[n.id] }
  }

  const slotsLeft = $derived(max ? max - (nets?.length || 0) : null)
</script>

<svelte:head><title>{t('networks.title')} · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>{t('networks.title')}</h1>
    <p>{t('networks.intro')}</p>
  </div>
  <div class="od-cluster">
    <span class="chip" id="usage" data-testid="net-count">{t('networks.usage', { used: nets?.length ?? me.usage.networks, max: limit(max) })}</span>
    {#if atLimit}<button class="btn btn-primary" type="button" disabled><Icon name="plus" />{t('networks.addNetwork')}</button>{:else}<a class="btn btn-primary" href="/networks/new"><Icon name="plus" />{t('networks.addNetwork')}</a>{/if}
  </div>
</div>

<div class="toolbar enter">
  <label class="search">
    <Icon name="search" /><span class="sr-only">{t('networks.search')}</span>
    <input id="page-search" type="search" placeholder={t('networks.searchPlaceholder')} autocomplete="off" bind:value={q} />
  </label>
  <div class="chips" role="group" aria-label={t('networks.filterBy')}>
    {#each FILTERS as [k, l]}
      <button class="chip-btn" type="button" aria-pressed={filter === k} onclick={() => (filter = k)}>{t(l)} <span class="n">{counts[k]}</span></button>
    {/each}
  </div>
</div>

{#if !nets}
  <div class="net-grid"><div class="skel" style="height:248px"></div><div class="skel" style="height:248px"></div></div>
{:else}
  <div class="net-grid" aria-live="polite">
    {#each list as n (n.id)}
      {@const on = n.status !== 'disconnected'}
      <article class="net-card" class:off={!on} id="net-{n.id}" style="--net:{n.color};--net-glow:{n.glow}" aria-labelledby="nh-{n.id}" data-testid="network-row">
        <div class="top">
          <div class="od-row">
            <span class="mono-badge" aria-hidden="true">{n.short}</span>
            <div class="od-field">
              <h3 id="nh-{n.id}" class="od-truncate">{n.name}</h3>
              <span class="host od-truncate">{n.host}:{n.port}{n.tls ? ' · TLS' : ''}</span>
            </div>
          </div>
          <span class="switch-hit"><Switch checked={on} busy={!!busy[n.id]} label={t(on ? 'networks.disconnectName' : 'networks.connectName', { name: n.name })} onchange={(v) => toggle(n, v)} /></span>
        </div>
        <div class="status-line"><StatusChip status={n.status} /><span class="chip nick"><Icon name="user" />{n.currentNick || n.nick}</span></div>
        {#if (n.status === 'reconnecting' || n.status === 'connecting') && n.lastError}
          <div class="err-note"><Icon name="alert" class="i-sm" /><span>{n.lastErrorAt ? t('networks.errorAt', { error: n.lastError.charAt(0).toUpperCase() + n.lastError.slice(1), time: clock(n.lastErrorAt) }) : n.lastError.charAt(0).toUpperCase() + n.lastError.slice(1)}. {n.retryIn ? t('networks.retryingIn', { wait: wait(n.retryIn) }) : t('networks.retrying')}{n.attempts > 1 ? ` · ${t('networks.attempt', { count: n.attempts })}` : ''}.{#if n.hint}<br /><span class="faint">{n.hint}</span>{/if} <a href="/networks/{n.id}#log">{t('networks.connectionLog')}</a></span></div>
        {/if}
        <div class="stats">
          <div class="od-stat"><span>{n.channels.length}</span><span>{t('networks.channels')}</span></div>
          <div class="od-stat"><span>{n.lag != null ? n.lag + ' ms' : '—'}</span><span>{t('networks.lag')}</span></div>
          <div class="od-stat"><span>{n.status === 'connected' ? since(n.connectedAt) : '—'}</span><span>{t('networks.uptime')}</span></div>
        </div>
        <div class="foot">
          <a class="btn btn-sm" href="/chat?net={n.id}" aria-disabled={!on ? 'true' : undefined} tabindex={on ? undefined : -1}><Icon name="message" />{t('networks.openChat')}{#if n.mentions}<span class="count mention">{n.mentions}</span>{:else if n.unread}<span class="count">{n.unread}</span>{/if}</a>
          <div class="od-cluster"><a class="btn btn-ghost btn-sm" href="/networks/{n.id}/health" aria-label={t('networks.healthName', { name: n.name })} title={t('networks.healthTip')} data-testid="health-link"><Icon name="chart" /></a><a class="btn btn-ghost btn-sm" href="/networks/{n.id}" aria-label={t('networks.editName', { name: n.name })}><Icon name="pencil" />{t('networks.edit')}</a></div>
        </div>
      </article>
    {/each}
    {#if filter === 'all' && !q && !atLimit}
      <a class="add-card" href="/networks/new"><span class="ic"><Icon name="plus" /></span><span class="od-stack"><span>{t('networks.addANetwork')}</span><span>{slotsLeft == null ? t('networks.noLimit') : t('networks.slotsLeft', { count: slotsLeft })}</span></span></a>
    {/if}
  </div>
  {#if !list.length && (filter !== 'all' || q)}
    <div class="empty card">
      <span class="ic"><Icon name="search" /></span><h3>{t('networks.noMatch')}</h3>
      <p>{t('networks.noMatchHelp')}</p>
      <button class="btn btn-sm" type="button" onclick={() => { q = ''; filter = 'all' }}>{t('networks.clearFilters')}</button>
    </div>
  {/if}
  {#if atLimit}
    <div class="callout"><Icon name="info" /><span><b>{t('networks.atLimit', { count: max })}</b> {t('networks.atLimitHelp')}</span></div>
  {/if}
{/if}



<style>
  .add-card {
    display: grid; place-items: center; align-content: center; gap: var(--s3); min-height: 248px; padding: var(--s5); border-radius: var(--r-card);
    border: 1px dashed var(--line-2); background: none; color: var(--muted); text-align: center;
  }
  .add-card:hover { border-color: var(--primary-dim); color: var(--fg); background: var(--primary-soft); }
  .add-card .ic { display: grid; place-items: center; width: 44px; height: 44px; border-radius: 12px; background: var(--row-hover); color: var(--primary); }
  .add-card .od-stack { --od-gap: 4px; }
  .add-card .od-stack span:first-child { font-weight: 600; color: var(--fg); }
  .add-card .od-stack span:last-child { font-size: var(--fs-sm); }
  :global(.net-card .status-line) { display: flex; align-items: center; justify-content: space-between; gap: var(--s2); }
  :global(.net-card .err-note) { display: flex; gap: var(--s2); padding: var(--s2) var(--s3); border-radius: var(--r-field); background: var(--warn-soft); color: var(--warn); font-size: var(--fs-xs); }
  :global(.net-card .err-note > span) { min-width: 0; overflow-wrap: anywhere; }
</style>
