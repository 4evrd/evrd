<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import NetworkForm from '$lib/components/NetworkForm.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog, esc } from '$lib/ui.svelte.js'
  import { limit } from '$lib/format.js'

  let nets = $state(null)
  let listErr = $state('')
  let busy = $state({}) // id -> 'toggle' | 'delete'
  let addCard = $state()
  let editing = $state(null) // the network being edited
  let editCard = $state()
  let timer

  async function load() {
    try { nets = await api.networks(); listErr = '' } catch (e) { listErr = e.message }
  }
  onMount(async () => {
    await load()
    timer = setInterval(load, 4000) // keep statuses fresh
    if (location.hash === '#add') { await tick(); focusAdd() }
    const m = location.hash.match(/^#edit-(\d+)$/)
    if (m) { const n = nets?.find((x) => x.id === Number(m[1])); if (n) edit(n) }
  })
  onDestroy(() => clearInterval(timer))

  const me = $derived(session.me)
  const atLimit = $derived(me.limits.maxNetworks > 0 && (nets?.length ?? me.usage.networks) >= me.limits.maxNetworks)

  function focusAdd() {
    addCard?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    addCard?.querySelector('input')?.focus({ preventScroll: true })
  }

  async function add(net) {
    const n = await api.addNetwork(net)
    await Promise.all([load(), refreshMe()])
    toast(`${n.name} added — connect it when you’re ready`)
  }

  async function edit(n) {
    editing = n
    await tick()
    editCard?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    editCard?.querySelector('input')?.focus({ preventScroll: true })
  }

  async function save(net) {
    const patch = { ...net }
    if (patch.password === undefined) delete patch.password
    const n = await api.updateNetwork(editing.id, patch)
    editing = null
    history.replaceState(null, '', location.pathname)
    await load()
    toast(n.reconnected ? `${n.name} saved — reconnecting to apply` : `${n.name} saved`)
  }

  async function toggle(n) {
    busy[n.id] = 'toggle'
    try {
      if (n.status === 'disconnected') { await api.connect(n.id); toast(`Connecting to ${n.name}…`) }
      else { await api.disconnect(n.id); toast(`Disconnected from ${n.name}`) }
      await load()
    } catch (e) { toast(e.message, 'err') } finally { delete busy[n.id] }
  }

  async function remove(n) {
    const ok = await confirmDialog({
      title: `Remove ${n.name}?`,
      body: `<p>evrd disconnects from <span class="code">${esc(n.host)}</span> and deletes this network’s saved history. Clients attached to it are dropped.</p>`,
      confirmLabel: 'Remove network', danger: true,
    })
    if (!ok) return
    busy[n.id] = 'delete'
    try {
      await api.removeNetwork(n.id)
      await Promise.all([load(), refreshMe()])
      toast(`${n.name} removed`)
    } catch (e) { toast(e.message, 'err') } finally { delete busy[n.id] }
  }
</script>

<svelte:head><title>Networks · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Networks</h1>
    <p>The IRC networks evrd stays connected to for you. Open one to chat in the browser, or attach any IRC client.</p>
  </div>
  <button class="btn btn-primary" type="button" onclick={focusAdd} disabled={atLimit}><Icon name="plus" />Add network</button>
</div>

<section class="card enter" aria-labelledby="list-h">
  <div class="section-head">
    <h2 id="list-h"><Icon name="globe" />Your networks</h2>
    <span class="chip" data-testid="net-count">{nets ? nets.length : me.usage.networks}&nbsp;of&nbsp;{limit(me.limits.maxNetworks)}</span>
  </div>
  <div class="rows" aria-live="polite">
    {#if listErr && !nets}
      <p class="desc">Couldn’t load your networks: {listErr}</p>
    {:else if !nets}
      <div class="skel"></div><div class="skel"></div>
    {:else if !nets.length}
      <div class="empty"><span class="ic"><Icon name="globe" /></span><h4>No networks yet</h4><p>Add one below — Libera.Chat, OFTC, or your own server.</p></div>
    {:else}
      <div class="row-head" aria-hidden="true" style="--cols:minmax(0,1fr) auto auto"><span>Network</span><span>Status</span><span></span></div>
      {#each nets as n (n.id)}
        <div class="row" class:removing={busy[n.id] === 'delete'} style="--cols:minmax(0,1fr) auto auto" data-testid="network-row">
          <span class="main">
            <span class="name"><a href="/networks/{n.id}">{n.name}</a></span>
            <span class="sub">{n.nick} · {n.host}:{n.port}{n.tls ? ' · TLS' : ''}{n.channels.length ? ' · ' + n.channels.join(' ') : ''}</span>
          </span>
          <span><StatusChip status={n.status} /></span>
          <span class="meta">
            <a class="btn btn-sm" href="/networks/{n.id}"><Icon name="message" />Open</a>
            <button class="btn btn-sm" type="button" aria-label="Edit {n.name}" onclick={() => edit(n)}><Icon name="pencil" /></button>
            <button class="btn btn-sm" type="button" disabled={!!busy[n.id]} onclick={() => toggle(n)}>
              {#if busy[n.id] === 'toggle'}<span class="spin" aria-hidden="true"></span>{/if}{n.status === 'disconnected' ? 'Connect' : 'Disconnect'}
            </button>
            <button class="btn btn-danger btn-sm" type="button" aria-label="Remove {n.name}" disabled={!!busy[n.id]} onclick={() => remove(n)}><Icon name="trash" /></button>
          </span>
        </div>
      {/each}
    {/if}
  </div>
</section>

{#if editing}
  {#key editing.id}
    <section class="card" aria-labelledby="edit-h" bind:this={editCard}>
      <h3 id="edit-h"><Icon name="pencil" />Edit {editing.name}</h3>
      {#if editing.status !== 'disconnected'}<p class="desc">Server, port, nick, identity and password changes reconnect {editing.name} to take effect.</p>{/if}
      <NetworkForm initial={editing} onsave={save} oncancel={() => { editing = null; history.replaceState(null, '', location.pathname) }} />
    </section>
  {/key}
{/if}

<section class="card enter" aria-labelledby="add-h" id="add" bind:this={addCard}>
  <h3 id="add-h"><Icon name="plus" />Add a network</h3>
  {#if atLimit}
    <div class="callout"><Icon name="info" /><div class="body"><p><b>You’ve reached your limit of {me.limits.maxNetworks} networks.</b> Remove one to add another, or ask an admin for a higher limit.</p></div></div>
  {:else}
    <p class="desc">evrd connects when you press Connect — nothing happens until then.</p>
    <NetworkForm onsave={add} />
  {/if}
</section>
