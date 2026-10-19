<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import Drawer from '$lib/components/Drawer.svelte'
  import Field from '$lib/components/Field.svelte'
  import { api } from '$lib/api.js'
  import { session, ircPort } from '$lib/session.svelte.js'
  import { toast, confirmDialog, openDrawer, closeDrawers } from '$lib/ui.svelte.js'
  import { date, ago, since, limit } from '$lib/format.js'
  import { check } from '$lib/validate.js'

  const me = $derived(session.me)
  const host = $derived(session.config.ircHost)
  const ports = $derived(session.config.ircPorts || [])
  const port = $derived(ircPort())
  const firstNet = $derived(session.nets[0]?.name || 'libera')

  let pws = $state(null)
  let attached = $state(null)
  let timer
  async function load() {
    try { pws = await api.clientPasswords() } catch (e) { if (!pws) toast(e.message, 'bad') }
    try { attached = await api.clients() } catch { /* keep */ }
  }
  onMount(() => { load(); timer = setInterval(async () => { try { attached = await api.clients() } catch { /* keep */ } }, 10000) })
  onDestroy(() => clearInterval(timer))

  let tab = $state('hex')
  const TABS = [['hex', 'HexChat'], ['irssi', 'irssi'], ['wee', 'WeeChat'], ['other', 'Other']]

  // new password drawer
  let name = $state('')
  let nameErr = $state('')
  let created = $state(null)
  let busy = $state(false)
  function openNew() { name = ''; nameErr = ''; created = null; openDrawer('pw-drawer') }
  async function create(e) {
    e.preventDefault()
    if (created) return
    nameErr = check('name', name)
    if (nameErr) { document.getElementById('pw-name')?.focus(); return }
    busy = true
    try {
      const cp = await api.createClientPassword(name.trim())
      created = cp
      await load()
      toast('Client password created')
      setTimeout(() => document.getElementById('pw-value')?.select(), 50)
    } catch (err) { nameErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' } finally { busy = false }
  }
  async function revoke(p) {
    const ok = await confirmDialog({ title: `Revoke “${p.name}”?`, body: 'Clients using this password can’t attach any more. Clients attached right now stay attached until they reconnect.', action: 'Revoke' })
    if (!ok) return
    try { await api.deleteClientPassword(p.id); await load(); toast(`Revoked ${p.name}`) } catch (e) { toast(e.message, 'bad') }
  }
  const pwText = $derived(created?.password || 'PASSWORD')
  const ircCount = $derived((attached?.clients || []).filter((c) => c.kind === 'irc').length)
</script>

<svelte:head><title>IRC clients · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>IRC clients</h1>
    <p>Connect HexChat, irssi, WeeChat or any other IRC client to evrd. You get missed messages when you attach and a live feed after that.</p>
  </div>
  <button class="btn btn-primary" type="button" onclick={openNew}><Icon name="key" />New client password</button>
</div>

{#if !me.hasPassword}
  <div class="callout warn enter" role="note" data-testid="sso-notice" style="margin-bottom:var(--s6)">
    <Icon name="alert" />
    <span class="od-fill"><b>Your {session.config.ssoLabel || 'SSO'} password won’t work in an IRC client.</b> You sign in to evrd with {session.config.ssoLabel || 'single sign-on'}, so IRC clients need a client password: create one and use it as the last part of the server password, <span class="code">{me.username}/{firstNet}:<i>client-password</i></span>.</span>
    {#if pws && !pws.length}<button class="btn btn-primary btn-sm" type="button" onclick={openNew}><Icon name="key" />Create one</button>{/if}
  </div>
{/if}

<div class="grid-main-side">
  <div class="side-stack" style="display:grid;gap:var(--s6)">
    <section class="card enter" aria-labelledby="conn-h">
      <div class="section-head">
        <div class="od-stack"><span class="caps">Connection settings</span><h2 id="conn-h">Point your client here</h2></div>
        {#if ports.some((p) => p.tls)}<span class="chip ok"><Icon name="shield" />TLS available</span>{/if}
      </div>
      <div class="kv-list">
        <div class="kv"><div class="od-field"><span class="k">Server</span><span class="v" data-testid="irc-host">{host}</span></div><Copy text={host} label="Copy server" /></div>
        <div class="kv"><div class="od-field"><span class="k">Port</span><span class="v">{ports.map((p) => `${p.port} (${p.tls ? 'TLS' : 'plain'})`).join(' · ') || port.port}</span></div><Copy text={String(port.port)} label="Copy port" /></div>
        <div class="kv"><div class="od-field"><span class="k">Server password</span><span class="v">{me.username}/<i>network</i>:<i>password</i></span></div><Copy text="{me.username}/{firstNet}:" label="Copy password format" /></div>
      </div>
      <p class="desc" style="margin-top:var(--s4)">{me.hasPassword ? 'Use your account password or a client password from below.' : `You sign in with ${session.config.ssoLabel}, so use a client password from below.`} Leave out <span class="code">/network</span> to attach to your first network{session.nets[0] ? `, ${session.nets[0].name}` : ''}.</p>
    </section>

    <section class="card enter" aria-labelledby="guide-h">
      <div class="section-head"><div class="od-stack"><span class="caps">Setup guide</span><h2 id="guide-h">Set up your client</h2></div></div>
      <div style="display:grid;gap:var(--s4)">
        <div class="tabs" role="tablist" aria-label="Client">
          {#each TABS as [k, l]}<button class="tab" role="tab" aria-selected={tab === k} onclick={() => (tab = k)}>{l}</button>{/each}
        </div>
        {#if tab === 'hex'}
          <div class="od-stack" style="--od-gap:12px">
            <p class="desc">Network List → Add, then Edit the new network:</p>
<pre class="snippet">Servers         <b>{host}/{port.port}</b>
Use SSL         <b>{port.tls ? 'on' : 'off'}</b>
Login method    <em>Server password (/PASS password)</em>
Password        <b>{me.username}/{firstNet}:</b><em>{pwText}</em></pre>
          </div>
        {:else if tab === 'irssi'}
<pre class="snippet">/network add <b>evrd-{firstNet}</b>
/server add {port.tls ? '-tls ' : ''}-network <b>evrd-{firstNet}</b> -password <b>{me.username}/{firstNet}:</b><em>{pwText}</em> <b>{host}</b> {port.port}
/connect <b>evrd-{firstNet}</b></pre>
        {:else if tab === 'wee'}
<pre class="snippet">/server add <b>evrd</b> <b>{host}</b>/{port.port}{port.tls ? ' -tls' : ''}
/set irc.server.evrd.password "<b>{me.username}/{firstNet}:</b><em>{pwText}</em>"
/connect <b>evrd</b></pre>
        {:else}
          <p class="desc">Any client that can send a server password (PASS) works. Use host <span class="code">{host}</span>, port <span class="code">{port.port}</span>{port.tls ? ' with TLS' : ''}, and the password format above. Add one server entry per network.</p>
        {/if}
        {#if port.tls && session.config?.ircSelfSigned}<p class="desc" style="font-size:var(--fs-xs)">If your client rejects the certificate, the server is using a self-signed one — allow it in the client’s TLS settings.</p>{/if}
      </div>
    </section>
  </div>

  <div class="side-stack" style="display:grid;gap:var(--s6)">
    <section class="card enter" aria-labelledby="pw-h">
      <div class="section-head" style="margin-bottom:var(--s3)"><div class="od-stack"><span class="caps">Client passwords</span><h2 id="pw-h">Per-device passwords</h2></div></div>
      <p class="desc" style="margin-bottom:var(--s4)">Give each device its own password. Revoke one and only that device is signed out.</p>
      <div class="rows">
        {#if !pws}
          <div class="skel"></div>
        {:else}
          {#each pws as p (p.id)}
            <div class="row pw-row">
              <span class="pic"><Icon name="key" /></span>
              <span class="od-field"><span class="name od-truncate">{p.name}</span><span class="sub">Created {date(p.createdAt)} · used {ago(p.lastUsedAt)}</span></span>
              <span></span>
              <button class="btn btn-danger-ghost btn-sm" type="button" aria-label="Revoke {p.name}" onclick={() => revoke(p)}>Revoke</button>
            </div>
          {:else}
            <div class="empty" style="padding:24px 8px"><span class="ic"><Icon name="key" /></span><h3>No client passwords yet</h3><p>{me.hasPassword ? 'Your account password works too, but a per-device password is safer to hand to a client.' : 'Create one for each client you use.'}</p><button class="btn btn-primary btn-sm" type="button" onclick={openNew}><Icon name="plus" />New client password</button></div>
          {/each}
        {/if}
      </div>
    </section>

    <section class="card tight enter" aria-labelledby="live-h">
      <div class="section-head" style="margin-bottom:var(--s3)">
        <h2 id="live-h" style="font-size:var(--fs-md)">Attached right now</h2>
        <span class="chip nick">{ircCount} of {limit(me.limits.maxClients)}</span>
      </div>
      <div class="rows">
        {#each attached?.clients || [] as c (c.id)}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="od-field"><span class="name">{c.kind === 'web' ? 'Web' : c.software || 'IRC client'}{c.device ? ' · ' + c.device : c.kind === 'web' ? ' · ' + c.software : ''}</span><span class="sub mono od-truncate">{c.networks.join(', ')}{c.ip ? ' · ' + c.ip : ''}</span></span>
            <span class="chip ok live"><span class="dot" aria-hidden="true"></span>{since(c.since)}</span>
          </div>
        {/each}
        {#if !(attached?.clients || []).some((c) => c.kind === 'web')}
          <!-- the server only sees open chat tabs; this browser is here too -->
          <div class="row" style="--cols:minmax(0,1fr) auto" data-testid="this-browser">
            <span class="od-field"><span class="name">Web · this browser</span><span class="sub od-truncate">signed in to the web app — open Chat to follow your networks live</span></span>
            <span class="chip ok live"><span class="dot" aria-hidden="true"></span>now</span>
          </div>
        {/if}
        {#if ircCount === 0}
          <p class="desc" style="font-size:var(--fs-sm)">No IRC clients attached right now.</p>
        {/if}
      </div>
    </section>
  </div>
</div>

<Drawer id="pw-drawer" title="New client password" sub="Name it after the device so you know which one to revoke later.">
  <form class="drawer-body form" id="pw-form" novalidate onsubmit={create}>
    <Field id="pw-name" label="Device name" required help="Up to 32 characters." err={nameErr}>
      <input class="input" id="pw-name" placeholder="e.g. Work laptop — HexChat" autocomplete="off" maxlength="64" bind:value={name} readonly={!!created} aria-invalid={!!nameErr} />
    </Field>
    {#if created}
      <div class="reveal-box" data-testid="new-secret">
        <div class="od-row"><Icon name="key" /><strong>Copy this password now</strong></div>
        <p class="desc" style="color:var(--fg)">evrd only stores a hash, so you won’t be able to see it again.</p>
        <div class="od-row">
          <label class="sr-only" for="pw-value">New password</label>
          <input class="input mono od-fill" id="pw-value" readonly value={created.password} />
          <Copy text={created.password} label="Copy password" size={44} />
        </div>
      </div>
    {/if}
  </form>
  {#snippet foot()}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>{created ? 'Done' : 'Cancel'}</button>
    {#if !created}<button class="btn btn-primary" type="submit" form="pw-form" disabled={busy}>{#if busy}<span class="spinner" aria-hidden="true"></span>Creating…{:else}<Icon name="plus" />Create password{/if}</button>{/if}
  {/snippet}
</Drawer>

<style>
  .pw-row { --cols: 36px minmax(0, 1fr) auto auto; }
  .pic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--nick); }
</style>
