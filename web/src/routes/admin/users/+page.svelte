<script>
  import { onMount, onDestroy } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import Drawer from '$lib/components/Drawer.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast, openDrawer, closeDrawers } from '$lib/ui.svelte.js'
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
    if (want) goto(`/admin/users/${want.id}`, { replaceState: true })
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
      <p>Everyone with an account on this bouncer. Open one to see their networks, channels and clients, and to set limits, reset passwords, suspend or remove them.</p>
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
              <tr tabindex="0" aria-label="Open {u.username}" data-testid="user-row" onclick={() => goto(`/admin/users/${u.id}`)} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); goto(`/admin/users/${u.id}`) } }}>
                <td><div class="user-cell"><span class="avatar" aria-hidden="true">{u.username[0].toUpperCase()}</span><span class="od-field"><a class="user-link" href="/admin/users/{u.id}" onclick={(e) => e.stopPropagation()}>{u.username}</a><span class="od-truncate">{u.sso ? 'Signs in with ' + session.config.ssoLabel : u.email || 'No email'}</span></span></div></td>
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
  .user-cell .user-link { font-weight: 500; color: inherit; text-decoration: none; }
  .user-cell .user-link:hover { text-decoration: underline; }
  .user-cell .od-field span:last-child { font-size: var(--fs-xs); color: var(--muted); }
  .use-cell { display: grid; gap: 6px; min-width: 96px; }
  .use-cell span:first-child { font-family: var(--font-mono); font-size: var(--fs-xs); }
  td.mono-cell { font-family: var(--font-mono); font-size: var(--fs-xs); color: var(--muted); white-space: nowrap; }
  :global(.tiles.admin .tile) { padding: var(--s4) var(--s5); }
</style>
