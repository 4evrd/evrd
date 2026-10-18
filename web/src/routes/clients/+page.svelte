<script>
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import Field from '$lib/components/Field.svelte'
  import { api } from '$lib/api.js'
  import { session, ircPort } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago, date, limit } from '$lib/format.js'

  const me = $derived(session.me)
  const host = $derived(session.config.ircHost)
  const port = $derived(ircPort())

  let list = $state(null)
  let listErr = $state('')
  let name = $state('')
  let nameErr = $state('')
  let busy = $state(false)
  let created = $state(null) // { name, password } — shown once
  let deleting = $state(-1)
  let nets = $state([])
  let tab = $state('irssi')
  let nameIn = $state()

  async function load() {
    try { list = await api.clientPasswords(); listErr = '' } catch (e) { listErr = e.message }
  }
  onMount(async () => { load(); try { nets = await api.networks() } catch { /* examples fall back */ } })

  const netName = $derived(nets[0]?.name || 'libera')
  const secret = $derived(created?.password || '<password>')
  const pass = $derived(`${me.username}/${netName}:${secret}`)

  async function create(e) {
    e.preventDefault()
    nameErr = name.trim() ? '' : 'Name it after the client or device, e.g. “laptop irssi”.'
    if (nameErr) { nameIn.focus(); return }
    busy = true
    try {
      const cp = await api.createClientPassword(name.trim())
      created = { name: cp.name, password: cp.password }
      name = ''
      await load()
    } catch (err) { nameErr = err.message; nameIn.focus() } finally { busy = false }
  }

  async function del(cp) {
    const ok = await confirmDialog({
      title: `Revoke “${cp.name}”?`,
      body: `<p>Clients using this password can’t attach any more. Clients attached right now stay attached until they reconnect.</p>`,
      confirmLabel: 'Revoke password', danger: true,
    })
    if (!ok) return
    deleting = cp.id
    try {
      await api.deleteClientPassword(cp.id)
      if (created && created.name === cp.name) created = null
      await load()
      toast('Client password revoked')
    } catch (err) { toast(err.message, 'err') } finally { deleting = -1 }
  }
</script>

<svelte:head><title>IRC clients · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>IRC clients</h1>
    <p>Attach irssi, WeeChat, HexChat or any IRC client to evrd. You get the backlog you missed, then the live conversation — and every attached client stays in sync.</p>
  </div>
</div>

<div class="grid-2 main-side enter">
  <section class="card frost connect" aria-labelledby="conn-h">
    <h3 id="conn-h"><Icon name="plug" />Connection settings</h3>
    <div class="kv-list">
      <div class="kv"><div class="text"><span class="k">Server</span><span class="v" data-testid="irc-host">{host}</span></div><Copy text={host} label="Copy server" /></div>
      <div class="kv"><div class="text"><span class="k">Port</span><span class="v">{port.port}</span></div><Copy text={String(port.port)} label="Copy port" /></div>
      <div class="kv"><div class="text"><span class="k">TLS</span><span class="v">{port.tls ? 'On' : 'Off'}</span></div></div>
      <div class="kv"><div class="text"><span class="k">Server password</span><span class="v">{me.username}/<i>network</i>:<i>password</i></span></div></div>
    </div>
    <p class="desc" style="font-size:14px"><i>network</i> is a network’s name, e.g. <span class="code">{netName}</span> — leave out <span class="code">/network</span> for your first one. Up to {limit(me.limits.maxClients)} clients can be attached at once.</p>
  </section>

  <section class="card" aria-labelledby="new-h">
    <h3 id="new-h"><Icon name="key" />New client password</h3>
    <p class="desc">{me.hasPassword ? 'Your account password works too, but a password per client is safer: revoke one without changing the others.' : `You sign in with ${session.config.ssoLabel}, so IRC clients need one of these.`}</p>
    {#if created}
      <div class="callout ok" role="status" data-testid="new-secret">
        <Icon name="check" />
        <div class="body">
          <p><b>Copy it now — it won’t be shown again.</b> Password for “{created.name}”:</p>
          <div class="secret"><span class="code">{created.password}</span><Copy text={created.password} label="Copy password" /></div>
        </div>
      </div>
    {/if}
    <form class="form" novalidate onsubmit={create}>
      <Field id="cp-name" label="Name" help="Which client or device it’s for." err={nameErr}>
        <input id="cp-name" class="input" placeholder="laptop irssi" maxlength="64" bind:value={name} bind:this={nameIn} aria-invalid={nameErr ? 'true' : undefined} aria-describedby="cp-name-help cp-name-err" />
      </Field>
      <div><button class="btn btn-primary" type="submit" disabled={busy}>
        {#if busy}<span class="spin" aria-hidden="true"></span>Creating…{:else}<Icon name="plus" />Create password{/if}
      </button></div>
    </form>
  </section>
</div>

<section class="card enter" aria-labelledby="list-h">
  <div class="section-head">
    <h2 id="list-h"><Icon name="lock" />Client passwords</h2>
    <span class="chip">{list ? list.length : 0}&nbsp;active</span>
  </div>
  <div class="rows">
    {#if listErr}
      <p class="desc">Couldn’t load them: {listErr}</p>
    {:else if !list}
      <div class="skel"></div>
    {:else if !list.length}
      <div class="empty"><span class="ic"><Icon name="key" /></span><h4>No client passwords yet</h4><p>Create one above for each client you use.</p></div>
    {:else}
      <div class="row-head" aria-hidden="true" style="--cols:minmax(0,1fr) auto auto"><span>Name</span><span>Last used</span><span></span></div>
      {#each list as cp (cp.id)}
        <div class="row" class:removing={deleting === cp.id} style="--cols:minmax(0,1fr) auto auto">
          <span class="main"><span class="name">{cp.name}</span><span class="sub">created {date(cp.createdAt)}</span></span>
          <span class="sub-cell">{ago(cp.lastUsedAt)}</span>
          <span class="meta"><button class="btn btn-danger btn-sm" type="button" disabled={deleting === cp.id} onclick={() => del(cp)} aria-label="Revoke {cp.name}">Revoke</button></span>
        </div>
      {/each}
    {/if}
  </div>
</section>

<section class="card enter" aria-labelledby="how-h">
  <div class="section-head">
    <h2 id="how-h"><Icon name="terminal" />Set up your client</h2>
    <div class="tabs" role="tablist" aria-label="IRC client">
      {#each [['irssi', 'irssi'], ['weechat', 'WeeChat'], ['hexchat', 'HexChat']] as [k, l]}
        <button class="tab" role="tab" type="button" aria-selected={tab === k} onclick={() => (tab = k)}>{l}</button>
      {/each}
    </div>
  </div>
  {#if tab === 'irssi'}
    <div class="console"><div class="console-body">
      <div class="line"><span class="prompt">/</span>network add evrd</div>
      <div class="line"><span class="prompt">/</span>server add -network evrd {port.tls ? '-tls ' : ''}-port {port.port} {host} {pass}</div>
      <div class="line"><span class="prompt">/</span>connect evrd</div>
    </div></div>
  {:else if tab === 'weechat'}
    <div class="console"><div class="console-body">
      <div class="line"><span class="prompt">/</span>server add evrd {host}/{port.port}{port.tls ? ' -tls' : ''}</div>
      <div class="line"><span class="prompt">/</span>set irc.server.evrd.password "{pass}"</div>
      <div class="line"><span class="prompt">/</span>connect evrd</div>
    </div></div>
  {:else}
    <div class="console"><div class="console-body">
      <div class="line sys">Network List → Add → Edit:</div>
      <div class="line">  Servers:   {host}/{port.port}</div>
      <div class="line">  {port.tls ? '[x]' : '[ ]'} Use SSL for all the servers on this network</div>
      <div class="line">  Login method: Server password (/PASS password)</div>
      <div class="line">  Password:  {pass}</div>
    </div></div>
  {/if}
  {#if port.tls}<p class="desc" style="font-size:14px">If your client rejects the certificate, the server is using a self-signed one — allow it in the client’s TLS settings.</p>{/if}
</section>

<style>
  .secret { display: flex; align-items: center; gap: var(--s2); flex-wrap: wrap; }
  .secret .code { font-size: 15px; padding: 6px 10px; }
  .sub-cell { font-size: 13px; color: var(--faint); white-space: nowrap; }
  .card .desc { overflow-wrap: anywhere; }
</style>
