<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import Drawer from '$lib/components/Drawer.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import BouncerSettings from '$lib/components/BouncerSettings.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog, openDrawer, closeDrawers, drawers } from '$lib/ui.svelte.js'
  import { ago, limit, ratio, level } from '$lib/format.js'
  import { check } from '$lib/validate.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  let users = $state(null)
  let err = $state('')
  let timer
  async function load() {
    try { users = await api.admin.users(); err = '' } catch (e) { err = e.message }
  }
  onMount(async () => {
    if (!isAdmin) return
    await load()
    timer = setInterval(load, 15000)
    const q = page.url.searchParams
    const want = q.get('user') ? users?.find((u) => u.username === q.get('user')) : q.get('id') ? users?.find((u) => u.id == q.get('id')) : null
    if (want) open(want)
    else if (location.hash === '#new') openNew()
  })
  onDestroy(() => clearInterval(timer))

  let filter = $state('all')
  let q = $state('')
  const FILTERS = [['all', 'All'], ['admin', 'Admins'], ['online', 'Online'], ['suspended', 'Suspended']]
  const list = $derived((users || []).filter((u) =>
    (filter === 'all' || (filter === 'admin' ? u.role === 'admin' : u.status === filter)) &&
    (!q.trim() || (u.username + ' ' + (u.email || '')).toLowerCase().includes(q.trim().toLowerCase()))))
  const stats = $derived({
    total: users?.length ?? 0,
    admins: users?.filter((u) => u.role === 'admin').length ?? 0,
    online: users?.filter((u) => u.status === 'online').length ?? 0,
    links: users?.reduce((a, u) => a + u.networks, 0) ?? 0,
    suspended: users?.filter((u) => u.suspended).length ?? 0,
  })
  const statusChip = (s) => s

  // ---- detail drawer
  let cur = $state(null)      // admin user detail (from /api/admin/users/{id})
  let lim = $state({ maxNetworks: '', maxClients: '', backlogLines: '' })
  let limErr = $state({})
  let saving = $state(false)
  let tempPw = $state('')
  async function open(u) {
    tempPw = ''
    limErr = {}
    // blank until this user's overrides load, so nothing typed is overwritten
    lim = { maxNetworks: '', maxClients: '', backlogLines: '' }
    cur = { ...u, networks: null }
    openDrawer('user-drawer')
    try {
      const d = await api.admin.user(u.id)
      cur = d
      lim = { maxNetworks: d.overrides.maxNetworks ?? '', maxClients: d.overrides.maxClients ?? '', backlogLines: d.overrides.backlogLines ?? '' }
    } catch (e) { toast(e.message, 'bad') }
  }
  const self = $derived(cur && cur.id === session.me.id)
  async function patch(body, msg) {
    try {
      const d = await api.admin.updateUser(cur.id, body)
      cur = { ...cur, ...d }
      if (self) await refreshMe()
      await load()
      toast(msg)
      return true
    } catch (e) { toast(e.message.charAt(0).toUpperCase() + e.message.slice(1), 'bad'); return false }
  }
  async function setAdmin(on) {
    if (!on && self && !(await confirmDialog({ title: 'Remove your own admin role?', body: 'You lose access to the admin pages straight away.', action: 'Remove admin' }))) return
    if (await patch({ role: on ? 'admin' : 'user' }, `${cur.username} is ${on ? 'now an admin' : 'no longer an admin'}`) && !on && self) location.href = '/'
  }
  async function saveLimits(e) {
    e.preventDefault()
    limErr = { maxNetworks: check('count', String(lim.maxNetworks)), maxClients: check('count', String(lim.maxClients)), backlogLines: check('count', String(lim.backlogLines)) }
    if (Object.values(limErr).some(Boolean)) return
    const num = (v) => (String(v).trim() === '' ? null : +v)
    saving = true
    if (await patch({ limits: { maxNetworks: num(lim.maxNetworks), maxClients: num(lim.maxClients), backlogLines: num(lim.backlogLines) } }, `Saved limits for ${cur.username}`)) closeDrawers()
    saving = false
  }
  async function saveBouncer(settings, denySetBindHost) {
    const d = await api.admin.updateUser(cur.id, { settings, denySetBindHost })
    cur = { ...cur, ...d, networks: cur.networks }
    return d.settings
  }
  async function resetPassword() {
    if (!(await confirmDialog({ title: `Reset ${cur.username}’s password?`, body: 'evrd makes a one-time temporary password for you to pass on. Their old password stops working and they’re signed out of the web app; they choose a new password at next sign-in.', action: 'Reset password', danger: false }))) return
    try { const r = await api.admin.resetPassword(cur.id); tempPw = r.password; toast(`Temporary password made for ${cur.username}`) } catch (e) { toast(e.message, 'bad') }
  }
  async function toggleSuspend() {
    if (cur.suspended) { await patch({ suspended: false }, `Restored ${cur.username}`); return }
    if (!(await confirmDialog({ title: `Suspend ${cur.username}?`, body: 'Their networks disconnect, attached clients are dropped and they can’t sign in until you restore them. Nothing is deleted.', action: 'Suspend' }))) return
    await patch({ suspended: true }, `Suspended ${cur.username}`)
  }
  async function del() {
    if (self) { toast('You can’t delete your own account here — use Account settings.', 'bad'); return }
    if (!(await confirmDialog({ title: `Delete ${cur.username}?`, body: 'This removes their account, networks, client passwords and chat history. It can’t be undone.', action: 'Delete user', typed: cur.username }))) return
    try { await api.admin.deleteUser(cur.id); closeDrawers(); await load(); toast(`Deleted ${cur.username}`) } catch (e) { toast(e.message, 'bad') }
  }

  // ---- new user drawer
  let nu = $state({ username: '', password: '', admin: false })
  let nuErr = $state({})
  let creating = $state(false)
  function openNew() { nu = { username: '', password: '', admin: false }; nuErr = {}; openDrawer('new-drawer') }
  async function create(e) {
    e.preventDefault()
    nuErr = { username: check('username', nu.username), password: check('password', nu.password) }
    const bad = Object.entries(nuErr).find(([, m]) => m)
    if (bad) { document.getElementById(bad[0] === 'username' ? 'n-user' : 'n-pass')?.focus(); return }
    creating = true
    try {
      const u = await api.admin.createUser({ username: nu.username.trim().toLowerCase(), password: nu.password, role: nu.admin ? 'admin' : 'user' })
      closeDrawers(); await load(); toast(`Created ${u.username}`)
    } catch (err2) {
      if (err2.status === 409 || /username/i.test(err2.message)) { nuErr.username = 'That username is taken — pick another.'; document.getElementById('n-user')?.focus() }
      else toast(err2.message, 'bad')
    } finally { creating = false }
  }
</script>

<svelte:head><title>Users · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>Admins only.</b> Your account doesn’t have access to user management.</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>Users</h1>
      <p>Everyone with an account on this bouncer. Set limits, reset passwords, suspend or remove accounts.</p>
    </div>
    <button class="btn btn-primary" type="button" onclick={openNew}><Icon name="plus" />New user</button>
  </div>

  <div class="tiles admin enter">
    <div class="tile"><span class="caps">Accounts</span><div class="od-stat"><span class="n" data-testid="tile-users">{stats.total}</span><span class="u">{stats.admins} admin{stats.admins === 1 ? '' : 's'}</span></div></div>
    <div class="tile"><span class="caps">Online now</span><div class="od-stat"><span class="n">{stats.online}</span><span class="u">with a client attached</span></div></div>
    <div class="tile"><span class="caps">Upstream links</span><div class="od-stat"><span class="n">{stats.links}</span><span class="u">across all users</span></div></div>
    <div class="tile"><span class="caps">Suspended</span><div class="od-stat"><span class="n">{stats.suspended}</span><span class="u">can’t sign in or attach</span></div></div>
  </div>

  <section class="card enter" aria-labelledby="acc-h">
    <div class="section-head">
      <h2 id="acc-h">Accounts</h2>
      <label class="search" style="width:min(320px,100%)">
        <Icon name="search" /><span class="sr-only">Search users</span>
        <input id="page-search" type="search" placeholder="Search users" autocomplete="off" bind:value={q} />
      </label>
    </div>
    <div class="chips" role="group" aria-label="Filter users" style="margin-bottom:var(--s3)">
      {#each FILTERS as [k, l]}<button class="chip-btn" type="button" aria-pressed={filter === k} onclick={() => (filter = k)}>{l}</button>{/each}
    </div>
    {#if err}<div class="callout bad"><Icon name="alert" /><span>Couldn’t load users: {err}</span></div>
    {:else if !users}<div class="skel"></div><div class="skel" style="margin-top:6px"></div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th scope="col">User</th><th scope="col">Role</th><th scope="col">Networks</th><th scope="col" class="hide-sm">Clients</th><th scope="col" class="hide-md">Last seen</th><th scope="col">Status</th></tr></thead>
          <tbody>
            {#each list as u (u.id)}
              <tr tabindex="0" aria-label="Open {u.username}" data-testid="user-row" onclick={() => open(u)} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(u) } }}>
                <td><div class="user-cell"><span class="avatar" aria-hidden="true">{u.username[0].toUpperCase()}</span><span class="od-field"><span>{u.username}</span><span class="od-truncate">{u.sso ? 'Signs in with ' + session.config.ssoLabel : u.email || 'No email'}</span></span></div></td>
                <td>{#if u.role === 'admin'}<span class="chip ok"><Icon name="crown" />Admin</span>{:else}<span class="chip">User</span>{/if}</td>
                <td><div class="use-cell"><span>{u.networks} / {limit(u.limits.maxNetworks)}</span><span class="ubar {level(u.networks, u.limits.maxNetworks)}" aria-hidden="true"><span style="transform:scaleX({ratio(u.networks, u.limits.maxNetworks)})"></span></span></div></td>
                <td class="mono-cell hide-sm">{u.clients} / {limit(u.limits.maxClients)}</td>
                <td class="mono-cell hide-md">{u.status === 'online' ? 'now' : ago(u.lastSeenAt || u.lastLoginAt)}</td>
                <td>{#if u.status === 'online'}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>Online</span>{:else if u.status === 'suspended'}<span class="chip bad"><Icon name="ban" />Suspended</span>{:else}<span class="chip"><span class="dot" aria-hidden="true"></span>Offline</span>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if !list.length}<div class="empty"><span class="ic"><Icon name="users" /></span><h3>No users match</h3><p>Try a different search or filter.</p></div>{/if}
    {/if}
  </section>
{/if}

<Drawer id="user-drawer" title={cur?.username || ''}>
  {#snippet head()}
    <div class="detail-hero">
      <span class="avatar" aria-hidden="true">{cur?.username?.[0]?.toUpperCase()}</span>
      <div class="od-stack"><h2 id="user-drawer-title">{cur?.username}</h2><p>{cur?.sso ? 'Signs in with ' + session.config.ssoLabel : cur?.email || 'No email'} · last seen {ago(cur?.lastSeenAt || cur?.lastLoginAt)}</p></div>
    </div>
  {/snippet}
  <form class="drawer-body form" id="limits-form" novalidate onsubmit={saveLimits}>
    {#if cur}
      <div class="od-stack" style="--od-gap:12px">
        <span class="caps">Limits</span>
        <div class="limit-grid">
          <Field id="l-net" label="Max networks" err={limErr.maxNetworks}><input class="input mono" id="l-net" inputmode="numeric" disabled={!cur.overrides} placeholder="default" bind:value={lim.maxNetworks} /></Field>
          <Field id="l-cli" label="Max clients" err={limErr.maxClients}><input class="input mono" id="l-cli" inputmode="numeric" disabled={!cur.overrides} placeholder="default" bind:value={lim.maxClients} /></Field>
          <Field id="l-back" label="Backlog lines" err={limErr.backlogLines}><input class="input mono" id="l-back" inputmode="numeric" disabled={!cur.overrides} placeholder="default" bind:value={lim.backlogLines} /></Field>
        </div>
        <span class="help">Empty uses the server default. 0 is unlimited (0 backlog lines keeps everything).</span>
      </div>
      <div class="od-stack" style="--od-gap:12px">
        <span class="caps">Networks</span>
        <div class="rows">
          {#if !cur.networks}<div class="skel"></div>
          {:else}
            {#each cur.networks as n}
              <div class="row" style="min-height:44px"><span class="od-field"><span class="name">{n.name}</span><span class="sub mono">{n.host}:{n.port}{n.lastError && n.status !== 'connected' ? ' · ' + n.lastError : ''}</span></span><StatusChip status={n.status} /></div>
            {:else}<p class="desc">No networks set up.</p>{/each}
          {/if}
        </div>
      </div>
      <div class="od-stack" style="--od-gap:12px">
        <span class="caps">Access</span>
        <div class="action-list">
          <div class="row"><span class="od-field"><span class="name" id="adm-l">Administrator</span><span class="sub">Can manage every user on this bouncer.</span></span><Switch checked={cur.role === 'admin'} labelledby="adm-l" onchange={setAdmin} /></div>
          <div class="row"><span class="od-field"><span class="name">Password</span><span class="sub">Makes a one-time temporary password for you to pass on.</span></span><button class="btn btn-sm" type="button" onclick={resetPassword}><Icon name="key" />Reset</button></div>
          {#if tempPw}
            <div class="reveal-box">
              <div class="od-row"><Icon name="key" /><strong>Temporary password for {cur.username}</strong></div>
              <p class="desc" style="color:var(--fg)">Share it privately. They’ll be asked to choose their own at next sign-in.</p>
              <div class="od-row"><input class="input mono od-fill" readonly value={tempPw} aria-label="Temporary password" /><Copy text={tempPw} label="Copy password" size={44} /></div>
            </div>
          {/if}
          {#if !self}
            <div class="row"><span class="od-field"><span class="name">{cur.suspended ? 'Suspended' : 'Suspend'}</span><span class="sub">{cur.suspended ? 'They can’t sign in or attach clients.' : 'Disconnects all networks and blocks sign-in.'}</span></span>
              <button class={cur.suspended ? 'btn btn-sm' : 'btn btn-danger-ghost btn-sm'} type="button" onclick={toggleSuspend}><Icon name={cur.suspended ? 'refresh' : 'ban'} />{cur.suspended ? 'Restore' : 'Suspend'}</button></div>
          {/if}
        </div>
      </div>
    {/if}
  </form>
  {#if cur?.settings}
    <details class="drawer-body bouncer-admin">
      <summary class="caps">Bouncer settings</summary>
      {#key cur.id}<BouncerSettings value={cur.settings} save={saveBouncer} admin compact />{/key}
    </details>
  {/if}
  {#snippet foot()}
    {#if !self}<button class="btn btn-danger-ghost left" type="button" onclick={del}><Icon name="trash" />Delete</button>{/if}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="limits-form" disabled={saving || !cur?.overrides}>{#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />Save limits{/if}</button>
  {/snippet}
</Drawer>

<Drawer id="new-drawer" title="New user" sub="They can sign in on the web and attach clients right away.">
  <form class="drawer-body form" id="new-form" novalidate onsubmit={create}>
    <Field id="n-user" label="Username" required help={'Used in server passwords: <span class="code">user/network:password</span>.'} err={nuErr.username}>
      <input class="input mono" id="n-user" autocomplete="off" spellcheck="false" placeholder="e.g. mira" bind:value={nu.username} aria-invalid={!!nuErr.username} />
    </Field>
    <Field id="n-pass" label="Temporary password" required help="They’ll be asked to change it at first sign-in." err={nuErr.password}>
      <input class="input" id="n-pass" type="password" autocomplete="new-password" bind:value={nu.password} aria-invalid={!!nuErr.password} />
    </Field>
    <div class="check-row"><span class="od-field"><span id="n-adm-l">Make administrator</span><span>Admins can manage every user.</span></span><Switch checked={nu.admin} labelledby="n-adm-l" onchange={(v) => (nu.admin = v)} /></div>
  </form>
  {#snippet foot()}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="new-form" disabled={creating}>{#if creating}<span class="spinner" aria-hidden="true"></span>Creating…{:else}<Icon name="plus" />Create user{/if}</button>
  {/snippet}
</Drawer>

<style>
  .user-cell { display: flex; align-items: center; gap: var(--s3); min-width: 180px; }
  .user-cell .od-field span:first-child { font-weight: 500; }
  .user-cell .od-field span:last-child { font-size: var(--fs-xs); color: var(--muted); }
  .use-cell { display: grid; gap: 6px; min-width: 96px; }
  .use-cell span:first-child { font-family: var(--font-mono); font-size: var(--fs-xs); }
  td.mono-cell { font-family: var(--font-mono); font-size: var(--fs-xs); color: var(--muted); white-space: nowrap; }
  :global(.tiles.admin .tile) { padding: var(--s4) var(--s5); }
  .detail-hero { display: flex; align-items: center; gap: var(--s4); }
  .detail-hero .avatar { width: 48px; height: 48px; font-size: var(--fs-lg); }
  .action-list { display: grid; gap: var(--s2); }
  .action-list .row { --cols: minmax(0, 1fr) auto; }
  .bouncer-admin { padding-top: 0; }
  .bouncer-admin summary { cursor: pointer; padding: var(--s2) 0; }
  .limit-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--s3); }
  @media (max-width: 639px) { .limit-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
