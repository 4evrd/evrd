<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Drawer from '$lib/components/Drawer.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshNets, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog, openDrawer, closeDrawers } from '$lib/ui.svelte.js'
  import { short, netColor, netGlow, since, clock, limit } from '$lib/format.js'
  import { check, splitChannels } from '$lib/validate.js'

  let nets = $state(null)
  let timer
  async function load() {
    try { nets = await api.networks(); session.nets = nets } catch (e) { if (!nets) toast(e.message, 'bad') }
  }
  onMount(async () => {
    await load()
    timer = setInterval(load, 4000)
    if (location.hash === '#add') openForm()
    const m = location.hash.match(/^#(?:net|edit)-(\d+)$/)
    if (m) {
      await tick()
      document.getElementById('net-' + m[1])?.scrollIntoView({ block: 'center' })
      if (location.hash.startsWith('#edit-')) { const n = nets.find((x) => x.id == m[1]); if (n) openForm(n) }
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
  const FILTERS = [['all', 'All'], ['connected', 'Connected'], ['reconnecting', 'Reconnecting'], ['disconnected', 'Off'], ['notls', 'Without TLS']]

  // connect / disconnect
  let busy = $state({})
  async function toggle(n, on) {
    busy[n.id] = true
    try {
      if (on) { await api.connect(n.id); toast(`Connecting to ${n.name}…`) }
      else { await api.disconnect(n.id); toast(`Disconnected from ${n.name}. Settings kept.`) }
      await load()
      refreshMe()
    } catch (e) { toast(e.message, 'bad') } finally { delete busy[n.id] }
  }

  // add / edit drawer
  let editing = $state(null)
  const blank = () => ({ name: '', host: '', port: '6697', tls: true, nick: '', username: '', password: '', channels: '', clearPassword: false })
  let f = $state(blank())
  let errs = $state({})
  let summary = $state([])
  let saving = $state(false)
  let peek = $state(false)
  let formErr = $state('')
  const RULE = { 'f-name': ['netname', () => f.name], 'f-host': ['host', () => f.host], 'f-port': ['port', () => f.port], 'f-nick': ['nick', () => f.nick], 'f-chans': ['channels', () => f.channels] }
  const LABEL = { 'f-name': 'Name', 'f-host': 'Server', 'f-port': 'Port', 'f-nick': 'Nickname', 'f-chans': 'Channels to join' }
  const validate = (id) => (errs[id] = check(RULE[id][0], RULE[id][1]()))
  // errors appear on blur and clear while typing (clearing on blur would shift
  // the form under a click on Save)
  const onBlur = (id) => () => { if (RULE[id][1]()) validate(id) }
  const onInput = (id) => () => { if (errs[id]) validate(id) }

  function openForm(n = null) {
    editing = n
    errs = {}; summary = []; formErr = ''; peek = false
    f = n
      ? { name: n.name, host: n.host, port: String(n.port), tls: n.tls, nick: n.nick, username: n.username, password: '', channels: n.channels.join(', '), clearPassword: false }
      : { ...blank(), nick: me.defaultNick || me.username }
    openDrawer('net-drawer')
  }
  function setTLS(on) {
    f.tls = on
    if (f.port === '6697' || f.port === '6667') f.port = on ? '6697' : '6667'
  }
  async function save(e) {
    e.preventDefault()
    formErr = ''
    for (const id in RULE) validate(id)
    summary = Object.entries(errs).filter(([, m]) => m)
    if (summary.length) { await tick(); document.getElementById(summary.length > 1 ? 'nf-summary' : summary[0][0])?.focus(); return }
    saving = true
    const body = { name: f.name.trim(), host: f.host.trim(), port: +f.port, tls: f.tls, nick: f.nick.trim(), username: f.username.trim(), channels: splitChannels(f.channels) }
    try {
      if (editing) {
        if (f.clearPassword) body.password = ''
        else if (f.password) body.password = f.password
        const n = await api.updateNetwork(editing.id, body)
        toast(n.reconnected ? `Saved ${n.name} — reconnecting to apply` : `Saved ${n.name}`)
      } else {
        body.password = f.password
        const n = await api.addNetwork(body)
        await api.connect(n.id).catch(() => {})
        toast(`Added ${n.name} — connecting…`)
        refreshMe()
      }
      closeDrawers()
      await load()
    } catch (err) {
      formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.'
    } finally { saving = false }
  }
  async function remove() {
    if (!editing) return
    const ok = await confirmDialog({ title: `Remove ${editing.name}?`, body: 'evrd disconnects, deletes its settings, and deletes this network’s chat history. It can’t be undone.', action: 'Remove network' })
    if (!ok) return
    try {
      await api.removeNetwork(editing.id)
      toast(`Removed ${editing.name}`)
      closeDrawers()
      await load()
      refreshMe()
    } catch (e) { toast(e.message, 'bad') }
  }
  const slotsLeft = $derived(max ? max - (nets?.length || 0) : null)
</script>

<svelte:head><title>Networks · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Networks</h1>
    <p>The IRC networks evrd stays connected to for you. Switch one off to disconnect without losing its settings.</p>
  </div>
  <div class="od-cluster">
    <span class="chip" id="usage" data-testid="net-count">{nets?.length ?? me.usage.networks} of {limit(max)} used</span>
    <button class="btn btn-primary" type="button" onclick={() => openForm()} disabled={atLimit}><Icon name="plus" />Add network</button>
  </div>
</div>

<div class="toolbar enter">
  <label class="search">
    <Icon name="search" /><span class="sr-only">Search networks</span>
    <input id="page-search" type="search" placeholder="Search by name, host or nick" autocomplete="off" bind:value={q} />
  </label>
  <div class="chips" role="group" aria-label="Filter by status">
    {#each FILTERS as [k, l]}
      <button class="chip-btn" type="button" aria-pressed={filter === k} onclick={() => (filter = k)}>{l} <span class="n">{counts[k]}</span></button>
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
          <span class="switch-hit"><Switch checked={on} busy={!!busy[n.id]} label="{on ? 'Disconnect' : 'Connect'} {n.name}" onchange={(v) => toggle(n, v)} /></span>
        </div>
        <div class="status-line"><StatusChip status={n.status} /><span class="chip nick"><Icon name="user" />{n.currentNick || n.nick}</span></div>
        {#if (n.status === 'reconnecting' || n.status === 'connecting') && n.lastError}
          <div class="err-note"><Icon name="alert" class="i-sm" /><span>{n.lastError.charAt(0).toUpperCase() + n.lastError.slice(1)}{n.lastErrorAt ? ` at ${clock(n.lastErrorAt)}` : ''}. {n.retryIn ? `Retrying in ${n.retryIn}s` : 'Retrying'}{n.attempts > 1 ? ` · attempt ${n.attempts}` : ''}.</span></div>
        {/if}
        <div class="stats">
          <div class="od-stat"><span>{n.channels.length}</span><span>Channels</span></div>
          <div class="od-stat"><span>{n.lag != null ? n.lag + ' ms' : '—'}</span><span>Lag</span></div>
          <div class="od-stat"><span>{n.status === 'connected' ? since(n.connectedAt) : '—'}</span><span>Uptime</span></div>
        </div>
        <div class="foot">
          <a class="btn btn-sm" href="/chat?net={n.id}" aria-disabled={!on ? 'true' : undefined} tabindex={on ? undefined : -1}><Icon name="message" />Open chat{#if n.mentions}<span class="count mention">{n.mentions}</span>{:else if n.unread}<span class="count">{n.unread}</span>{/if}</a>
          <div class="od-cluster"><button class="btn btn-ghost btn-sm" type="button" onclick={() => openForm(n)} aria-label="Edit {n.name}"><Icon name="pencil" />Edit</button></div>
        </div>
      </article>
    {/each}
    {#if filter === 'all' && !q && !atLimit}
      <button class="add-card" type="button" onclick={() => openForm()}><span class="ic"><Icon name="plus" /></span><span class="od-stack"><span>Add a network</span><span>{slotsLeft == null ? 'No limit on your plan' : `${slotsLeft} slot${slotsLeft === 1 ? '' : 's'} left on your plan`}</span></span></button>
    {/if}
  </div>
  {#if !list.length && (filter !== 'all' || q)}
    <div class="empty card">
      <span class="ic"><Icon name="search" /></span><h3>No networks match</h3>
      <p>Nothing matches that search and filter. Clear them to see all your networks.</p>
      <button class="btn btn-sm" type="button" onclick={() => { q = ''; filter = 'all' }}>Clear filters</button>
    </div>
  {/if}
  {#if atLimit}
    <div class="callout"><Icon name="info" /><span><b>You’ve reached your limit of {max} networks.</b> Remove one to add another, or ask an admin for more.</span></div>
  {/if}
{/if}

<Drawer id="net-drawer" title={editing ? `Edit ${editing.name}` : 'Add a network'}
  sub={editing ? 'Server, nick and password changes reconnect it.' : 'evrd connects as soon as you save, and stays connected.'}>
  <form class="drawer-body form" id="net-form" novalidate onsubmit={save} aria-label={editing ? `Edit ${editing.name}` : 'Add a network'}>
    <div class="err-summary" class:show={summary.length > 1} role="alert" id="nf-summary" tabindex="-1">
      {#if summary.length > 1}<strong>Fix {summary.length} things before saving:</strong>
        <ul>{#each summary as [id, m]}<li><a href="#{id}" onclick={(e) => { e.preventDefault(); document.getElementById(id)?.focus() }}>{LABEL[id]}</a> — {m}</li>{/each}</ul>{/if}
    </div>
    {#if formErr}<div class="callout bad" role="alert"><Icon name="alert" /><span>{formErr}</span></div>{/if}
    <div class="form-grid">
      <Field id="f-name" label="Name" required err={errs['f-name']} span>
        <input class="input" id="f-name" placeholder="libera" autocomplete="off" bind:value={f.name} onblur={onBlur('f-name')} oninput={onInput('f-name')} aria-invalid={!!errs['f-name']} />
      </Field>
      <Field id="f-host" label="Server" required err={errs['f-host']}>
        <input class="input mono" id="f-host" placeholder="irc.libera.chat" autocomplete="off" spellcheck="false" bind:value={f.host} onblur={onBlur('f-host')} oninput={onInput('f-host')} aria-invalid={!!errs['f-host']} />
      </Field>
      <Field id="f-port" label="Port" required err={errs['f-port']}>
        <input class="input mono" id="f-port" inputmode="numeric" bind:value={f.port} onblur={onBlur('f-port')} oninput={onInput('f-port')} aria-invalid={!!errs['f-port']} />
      </Field>
      <div class="check-row span-2">
        <span class="od-field"><span id="tls-l">Use TLS</span><span>Encrypts the connection. Most networks use port 6697 for TLS.</span></span>
        <Switch id="f-tls" checked={f.tls} labelledby="tls-l" onchange={setTLS} />
      </div>
    </div>
    <hr class="divider" />
    <div class="form-grid">
      <Field id="f-nick" label="Nickname" required err={errs['f-nick']}>
        <input class="input mono" id="f-nick" autocomplete="off" spellcheck="false" bind:value={f.nick} onblur={onBlur('f-nick')} oninput={onInput('f-nick')} aria-invalid={!!errs['f-nick']} />
      </Field>
      <Field id="f-user" label="Username" help="Optional. The ident part of nick!user@host.">
        <input class="input mono" id="f-user" placeholder="Same as nickname" autocomplete="off" spellcheck="false" bind:value={f.username} />
      </Field>
      <Field id="f-sasl" label="SASL password" span help={editing?.hasPassword ? 'A password is saved. Type a new one to replace it.' : 'Logs you in to NickServ when evrd connects.'}>
        <div class="input-group">
          <input class="input mono" id="f-sasl" type={peek ? 'text' : 'password'} placeholder={editing?.hasPassword ? '•••••••• (unchanged)' : 'Leave empty if you don’t use NickServ'} autocomplete="new-password" bind:value={f.password} disabled={f.clearPassword} />
          <button class="icon-btn" type="button" aria-label={peek ? 'Hide password' : 'Show password'} aria-pressed={peek} style="width:44px;height:44px" onclick={() => (peek = !peek)}><Icon name={peek ? 'eyeOff' : 'eye'} /></button>
        </div>
      </Field>
      {#if editing?.hasPassword}
        <div class="check-row span-2"><span class="od-field"><span id="clr-l">Remove the saved password</span><span>evrd connects without SASL.</span></span><Switch checked={f.clearPassword} labelledby="clr-l" onchange={(v) => (f.clearPassword = v)} /></div>
      {/if}
      <Field id="f-chans" label="Channels to join" span help="Separate with commas. evrd rejoins these after every reconnect; /join and /part update them too." err={errs['f-chans']}>
        <textarea class="textarea mono" id="f-chans" placeholder="#evrd, #go-nuts" spellcheck="false" bind:value={f.channels} onblur={onBlur('f-chans')} oninput={onInput('f-chans')} aria-invalid={!!errs['f-chans']}></textarea>
      </Field>
    </div>
  </form>
  {#snippet foot()}
    {#if editing}<button class="btn btn-danger-ghost left" type="button" onclick={remove}><Icon name="trash" />Remove</button>{/if}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="net-form" disabled={saving}>
      {#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />{editing ? 'Save changes' : 'Add and connect'}{/if}
    </button>
  {/snippet}
</Drawer>

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
</style>
