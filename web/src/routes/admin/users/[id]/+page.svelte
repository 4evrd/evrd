<script>
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog, esc } from '$lib/ui.svelte.js'
  import { date, ago, device, limit, cap } from '$lib/format.js'
  import { passwordError, limitError } from '$lib/validate.js'

  const id = Number(page.params.id)
  const isAdmin = $derived(session.me?.role === 'admin')
  const self = $derived(session.me?.id === id)
  let u = $state(null)
  let err = $state('')
  let defaults = $state(null)

  // editable copies
  let role = $state('user')
  let email = $state('')
  let lim = $state({ maxNetworks: '', maxClients: '', backlogLines: '' })
  let limErr = $state({})
  let reason = $state('')
  let pw = $state(''), pwErr = $state('')
  let busy = $state('')

  function take(d) {
    u = d
    role = d.role
    email = d.email
    const o = d.overrides
    lim = { maxNetworks: o.maxNetworks ?? '', maxClients: o.maxClients ?? '', backlogLines: o.backlogLines ?? '' }
    reason = d.suspendedReason || ''
  }
  async function load() {
    try { take(await api.admin.user(id)); err = '' } catch (e) { err = e.status === 404 ? 'No such user.' : e.message }
  }
  onMount(async () => {
    if (!isAdmin) return
    await load()
    try { defaults = (await api.admin.overview()).defaults } catch { /* placeholders fall back */ }
  })

  async function patch(body, okMsg) {
    try {
      take({ ...u, ...(await api.admin.updateUser(id, body)) })
      if (self) await refreshMe()
      toast(okMsg)
      return true
    } catch (e) { toast(cap(e.message), 'err'); return false }
  }

  async function saveAccount(e) {
    e.preventDefault()
    if (role !== u.role && self && role === 'user') {
      const ok = await confirmDialog({ title: 'Remove your own admin role?', body: '<p>You’ll lose access to this page straight away.</p>', confirmLabel: 'Remove admin', danger: true })
      if (!ok) return
    }
    busy = 'account'
    const ok = await patch({ role, email: email.trim() }, 'Account saved')
    busy = ''
    if (ok && self && role === 'user') goto('/')
  }

  async function saveLimits(e) {
    e.preventDefault()
    limErr = Object.fromEntries(Object.entries(lim).map(([k, v]) => [k, limitError(v)]))
    if (Object.values(limErr).some(Boolean)) return
    const num = (v) => (v === '' || v == null ? null : Number(v))
    busy = 'limits'
    await patch({ limits: { maxNetworks: num(lim.maxNetworks), maxClients: num(lim.maxClients), backlogLines: num(lim.backlogLines) } }, 'Limits saved')
    busy = ''
  }

  async function suspend() {
    const ok = await confirmDialog({
      title: `Suspend ${u.username}?`,
      body: `<p>They’re signed out of the web app, their IRC clients are dropped and evrd disconnects from their networks. Their data is kept, and lifting the suspension reconnects everything.</p>`,
      confirmLabel: 'Suspend', danger: true,
    })
    if (!ok) return
    busy = 'suspend'
    await patch({ suspended: true, suspendedReason: reason.trim() }, `${u.username} suspended`)
    busy = ''
  }
  async function unsuspend() {
    busy = 'suspend'
    await patch({ suspended: false }, `${u.username} can sign in again`)
    busy = ''
  }

  async function setPassword(e) {
    e.preventDefault()
    pwErr = passwordError(pw)
    if (pwErr) return
    busy = 'pw'
    try {
      await api.admin.setPassword(id, pw)
      pw = ''
      await load()
      toast('Password set — their other sessions were signed out')
    } catch (e2) { pwErr = cap(e2.message) } finally { busy = '' }
  }

  async function revokeAll() {
    busy = 'sessions'
    try { await api.admin.revokeSessions(id); await load(); toast('Signed out of the web app everywhere') }
    catch (e) { toast(e.message, 'err') } finally { busy = '' }
  }

  async function del() {
    const ok = await confirmDialog({
      title: `Delete ${u.username}?`,
      body: `<p>This disconnects their networks and deletes their account, ${u.networks.length} network${u.networks.length === 1 ? '' : 's'}, saved history and client passwords. It can’t be undone.</p>${u.sso ? `<p>Their ${esc(session.config.ssoLabel)} sign-in isn’t affected — signing in again creates a fresh, empty account.</p>` : ''}`,
      confirmLabel: 'Delete user', danger: true, requireText: u.username,
    })
    if (!ok) return
    try { await api.admin.deleteUser(id); toast(`${u.username} deleted`); goto('/admin') }
    catch (e) { toast(cap(e.message), 'err') }
  }

  const ph = (k) => (defaults ? `default: ${limit(defaults[k])}` : 'default')
</script>

<svelte:head><title>{u ? u.username : 'User'} · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout err enter" role="alert"><Icon name="lock" /><div class="body"><p><b>Admins only.</b></p></div></div>
{:else if err}
  <div class="empty enter"><span class="ic"><Icon name="user" /></span><h4>{err}</h4><a class="btn btn-sm" href="/admin">Back to users</a></div>
{:else if !u}
  <div class="skel"></div><div class="skel"></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/admin"><Icon name="chevron" class="flip" />All users</a>
      <h1>{u.username}
        {#if u.suspended}<span class="chip bad"><Icon name="ban" />Suspended</span>{:else}<span class="chip ok"><span class="dot" aria-hidden="true"></span>Active</span>{/if}
        {#if u.role === 'admin'}<span class="chip ok"><Icon name="crown" />Admin</span>{/if}
      </h1>
      <p>{u.sso ? `Signs in with ${session.config.ssoLabel}` : 'Signs in with a password'} · joined {date(u.createdAt)} · last sign-in {ago(u.lastLoginAt)}</p>
    </div>
  </div>

  {#if u.suspended}
    <div class="callout err enter" role="status"><Icon name="ban" /><div class="body"><p><b>Suspended{u.suspendedReason ? ':' : '.'}</b> {u.suspendedReason}</p><div><button class="btn btn-sm" type="button" disabled={busy === 'suspend'} onclick={unsuspend}>{#if busy === 'suspend'}<span class="spin" aria-hidden="true"></span>{/if}Lift suspension</button></div></div></div>
  {/if}

  <div class="grid-2 enter">
    <section class="card" aria-labelledby="acc-h">
      <h3 id="acc-h"><Icon name="user" />Account</h3>
      <form class="form" novalidate onsubmit={saveAccount}>
        <Field id="u-role" label="Role" help="Admins can manage every user, including other admins.">
          <select id="u-role" class="input" bind:value={role} aria-describedby="u-role-help"><option value="user">User</option><option value="admin">Admin</option></select>
        </Field>
        <Field id="u-email" label="Email" help={u.sso ? `Updated from ${session.config.ssoLabel} at each sign-in.` : ''}>
          <input id="u-email" class="input" type="email" bind:value={email} aria-describedby="u-email-help" />
        </Field>
        <div><button class="btn btn-primary" type="submit" disabled={busy === 'account' || (role === u.role && email === u.email)}>
          {#if busy === 'account'}<span class="spin" aria-hidden="true"></span>Saving…{:else}Save{/if}
        </button></div>
      </form>
    </section>

    <section class="card" aria-labelledby="lim-h">
      <h3 id="lim-h"><Icon name="sliders" />Limits</h3>
      <p class="desc">Empty uses the server default. 0 is unlimited.</p>
      <form class="form" novalidate onsubmit={saveLimits}>
        <div class="lim-grid">
          <Field id="l-nets" label="Networks" err={limErr.maxNetworks}>
            <input id="l-nets" class="input" inputmode="numeric" placeholder={ph('maxNetworks')} bind:value={lim.maxNetworks} aria-describedby="l-nets-err" />
          </Field>
          <Field id="l-cli" label="Attached clients" err={limErr.maxClients}>
            <input id="l-cli" class="input" inputmode="numeric" placeholder={ph('maxClients')} bind:value={lim.maxClients} aria-describedby="l-cli-err" />
          </Field>
          <Field id="l-log" label="Backlog lines" err={limErr.backlogLines}>
            <input id="l-log" class="input" inputmode="numeric" placeholder={ph('backlogLines')} bind:value={lim.backlogLines} aria-describedby="l-log-err" />
          </Field>
        </div>
        <p class="desc eff">In effect: {limit(u.limits.maxNetworks)} networks ({u.usage.networks} used) · {limit(u.limits.maxClients)} clients ({u.usage.clients} attached) · {limit(u.limits.backlogLines)} lines per channel</p>
        <div><button class="btn btn-primary" type="submit" disabled={busy === 'limits'}>
          {#if busy === 'limits'}<span class="spin" aria-hidden="true"></span>Saving…{:else}Save limits{/if}
        </button></div>
      </form>
    </section>
  </div>

  <section class="card enter" aria-labelledby="nets-h">
    <div class="section-head"><h2 id="nets-h"><Icon name="globe" />Networks</h2><span class="chip">{u.networks.length}</span></div>
    <div class="rows">
      {#each u.networks as n (n.id)}
        <div class="row" style="--cols:minmax(0,1fr) auto">
          <span class="main"><span class="name">{n.name}</span><span class="sub">{n.nick} · {n.host}:{n.port}{n.tls ? ' · TLS' : ''}{n.channels.length ? ' · ' + n.channels.join(' ') : ''}</span></span>
          <span class="meta"><StatusChip status={n.status} /></span>
        </div>
      {:else}
        <p class="desc">No networks.</p>
      {/each}
    </div>
  </section>

  <div class="grid-2 enter">
    <section class="card" aria-labelledby="ses-h">
      <div class="card-head">
        <h3 id="ses-h"><Icon name="monitor" />Web sessions</h3>
        {#if u.sessions.length}<button class="btn btn-sm" type="button" disabled={busy === 'sessions'} onclick={revokeAll}>Sign out everywhere</button>{/if}
      </div>
      <div class="rows">
        {#each u.sessions as s (s.id)}
          <div class="row" style="--cols:minmax(0,1fr)">
            <span class="main"><span class="name">{device(s.userAgent)}</span><span class="sub">{s.ip || 'unknown IP'} · {s.method === 'oidc' ? 'SSO' : 'password'} · active {ago(s.lastSeenAt)}</span></span>
          </div>
        {:else}
          <p class="desc">Not signed in anywhere.</p>
        {/each}
      </div>
      <p class="desc" style="font-size:14px">{u.clientPasswords} IRC client password{u.clientPasswords === 1 ? '' : 's'} — the user manages those.</p>
    </section>

    <section class="card" aria-labelledby="pw-h">
      <h3 id="pw-h"><Icon name="key" />{u.hasPassword ? 'Reset password' : 'Set a password'}</h3>
      <p class="desc">{u.hasPassword ? 'Replaces their password and signs them out of the web app.' : u.sso ? `They sign in with ${session.config.ssoLabel}. A password would also let them sign in here and attach IRC clients with it.` : 'They have no password, so they can’t sign in yet.'}</p>
      <form class="form" novalidate onsubmit={setPassword}>
        <Field id="u-pw" label="New password" help="At least 8 characters. Share it with them privately." err={pwErr}>
          <input id="u-pw" class="input" type="text" autocomplete="new-password" spellcheck="false" bind:value={pw} aria-describedby="u-pw-help u-pw-err" />
        </Field>
        <div><button class="btn" type="submit" disabled={busy === 'pw' || !pw}>
          {#if busy === 'pw'}<span class="spin" aria-hidden="true"></span>Saving…{:else}Set password{/if}
        </button></div>
      </form>
    </section>
  </div>

  <section class="card danger enter" aria-labelledby="danger-h">
    <h3 id="danger-h"><Icon name="alert" />Suspend or delete</h3>
    {#if !self}
      {#if !u.suspended}
        <div class="form-row host">
          <Field id="u-reason" label="Suspension reason" help="Shown to admins only.">
            <input id="u-reason" class="input" placeholder="e.g. spam from this account" bind:value={reason} aria-describedby="u-reason-help" />
          </Field>
          <div class="btn-cell"><button class="btn btn-danger" type="button" disabled={busy === 'suspend'} onclick={suspend}><Icon name="ban" />Suspend</button></div>
        </div>
      {/if}
      <div class="od-cluster"><button class="btn btn-danger" type="button" onclick={del}><Icon name="trash" />Delete user</button></div>
    {:else}
      <p class="desc">This is you. Delete your own account from the <a href="/account">Account</a> page.</p>
    {/if}
  </section>
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--faint); text-decoration: none; }
  .back:hover { color: var(--fg); }
  .back :global(.flip) { transform: rotate(180deg); width: 14px; height: 14px; }
  .chip :global(svg.i) { width: 14px; height: 14px; }
  .lim-grid { display: grid; gap: var(--s3); grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 640px) { .lim-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
  .eff { font-size: 14px !important; }
</style>
