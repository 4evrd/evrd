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
  import { t } from '$lib/i18n.svelte.js'

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
  const FILTERS = $derived([['all', t('adminUsers.filterAll')], ['admin', t('adminUsers.filterAdmins')], ['online', t('adminUsers.filterOnline')], ['suspended', t('adminUsers.filterSuspended')]])
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
      closeDrawers(); await load(); toast(t('adminUsers.created', { name: u.username }))
    } catch (err2) {
      if (err2.status === 409 || /username/i.test(err2.message)) { nuErr.username = t('adminUsers.usernameTaken'); document.getElementById('n-user')?.focus() }
      else toast(err2.message, 'bad')
    } finally { creating = false }
  }
</script>

<svelte:head><title>{t('adminUsers.pageTitle')}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('adminUsers.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('adminUsers.heading')}</h1>
      <p>{t('adminUsers.intro')}</p>
    </div>
    <button class="btn btn-primary" type="button" onclick={openNew}><Icon name="plus" />{t('adminUsers.newUser')}</button>
  </div>

  <div class="tiles admin enter">
    <div class="tile"><span class="caps">{t('admin.accounts')}</span><div class="od-stat"><span class="n" data-testid="tile-users">{stats.total}</span><span class="u">{t('admin.nAdmins', { count: stats.admins })}</span></div></div>
    <div class="tile"><span class="caps">{t('admin.onlineNow')}</span><div class="od-stat"><span class="n">{stats.online}</span><span class="u">{t('adminUsers.withClient')}</span></div></div>
    <div class="tile"><span class="caps">{t('admin.upstreamLinks')}</span><div class="od-stat"><span class="n">{stats.links}</span><span class="u">{t('adminUsers.acrossAllUsers')}</span></div></div>
    <div class="tile"><span class="caps">{t('admin.suspended')}</span><div class="od-stat"><span class="n">{stats.suspended}</span><span class="u">{t('adminUsers.cantSignIn')}</span></div></div>
  </div>

  <section class="card enter" aria-labelledby="acc-h">
    <div class="section-head">
      <h2 id="acc-h">{t('admin.accounts')}</h2>
      <label class="search" style="width:min(320px,100%)">
        <Icon name="search" /><span class="sr-only">{t('adminUsers.search')}</span>
        <input id="page-search" type="search" placeholder={t('adminUsers.search')} autocomplete="off" bind:value={q} />
      </label>
    </div>
    <div class="chips" role="group" aria-label={t('adminUsers.filterLabel')} style="margin-bottom:var(--s3)">
      {#each FILTERS as [k, l]}<button class="chip-btn" type="button" aria-pressed={filter === k} onclick={() => (filter = k)}>{l}</button>{/each}
    </div>
    {#if err}<div class="callout bad"><Icon name="alert" /><span>{t('adminUsers.loadFailed', { error: err })}</span></div>
    {:else if !users}<div class="skel"></div><div class="skel" style="margin-top:6px"></div>
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th scope="col">{t('adminUsers.colUser')}</th><th scope="col">{t('adminUsers.colRole')}</th><th scope="col">{t('adminUsers.colNetworks')}</th><th scope="col" class="hide-sm">{t('adminUsers.colClients')}</th><th scope="col" class="hide-md">{t('adminUsers.colLastSeen')}</th><th scope="col">{t('adminUsers.colStatus')}</th></tr></thead>
          <tbody>
            {#each list as u (u.id)}
              <tr tabindex="0" aria-label={t('adminUsers.openUser', { name: u.username })} data-testid="user-row" onclick={() => goto(`/admin/users/${u.id}`)} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); goto(`/admin/users/${u.id}`) } }}>
                <td><div class="user-cell"><span class="avatar" aria-hidden="true">{u.username[0].toUpperCase()}</span><span class="od-field"><a class="user-link" href="/admin/users/{u.id}" onclick={(e) => e.stopPropagation()}>{u.username}</a><span class="od-truncate">{u.sso ? t('adminUsers.signsInWith', { provider: session.config.ssoLabel }) : u.email || t('adminUsers.noEmail')}</span></span></div></td>
                <td>{#if u.role === 'admin'}<span class="chip ok"><Icon name="crown" />{t('adminUsers.roleAdmin')}</span>{:else}<span class="chip">{t('adminUsers.roleUser')}</span>{/if}</td>
                <td><div class="use-cell"><span>{u.networks} / {limit(u.limits.maxNetworks)}</span><span class="ubar {level(u.networks, u.limits.maxNetworks)}" aria-hidden="true"><span style="transform:scaleX({ratio(u.networks, u.limits.maxNetworks)})"></span></span></div></td>
                <td class="mono-cell hide-sm">{u.clients} / {limit(u.limits.maxClients)}</td>
                <td class="mono-cell hide-md">{u.status === 'online' ? t('adminUsers.now') : ago(u.lastSeenAt || u.lastLoginAt)}</td>
                <td>{#if u.status === 'online'}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('adminUsers.online')}</span>{:else if u.status === 'suspended'}<span class="chip bad"><Icon name="ban" />{t('admin.suspended')}</span>{:else}<span class="chip"><span class="dot" aria-hidden="true"></span>{t('adminUsers.offline')}</span>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if !list.length}<div class="empty"><span class="ic"><Icon name="users" /></span><h3>{t('adminUsers.noMatch')}</h3><p>{t('adminUsers.noMatchHelp')}</p></div>{/if}
    {/if}
  </section>
{/if}

<Drawer id="new-drawer" title={t('adminUsers.newUser')} sub={t('adminUsers.newUserSub')}>
  <form class="drawer-body form" id="new-form" novalidate onsubmit={create}>
    <Field id="n-user" label={t('adminUsers.username')} required help={t('adminUsers.usernameHelp', { format: '<span class="code">user/network:password</span>' })} err={nuErr.username}>
      <input class="input mono" id="n-user" autocomplete="off" spellcheck="false" placeholder={t('adminUsers.usernamePlaceholder')} bind:value={nu.username} aria-invalid={!!nuErr.username} />
    </Field>
    <Field id="n-pass" label={t('adminUsers.tempPassword')} required help={t('adminUsers.tempPasswordHelp')} err={nuErr.password}>
      <input class="input" id="n-pass" type="password" autocomplete="new-password" bind:value={nu.password} aria-invalid={!!nuErr.password} />
    </Field>
    <div class="check-row"><span class="od-field"><span id="n-adm-l">{t('adminUsers.makeAdmin')}</span><span>{t('adminUsers.makeAdminHelp')}</span></span><Switch checked={nu.admin} labelledby="n-adm-l" onchange={(v) => (nu.admin = v)} /></div>
  </form>
  {#snippet foot()}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>{t('common.cancel')}</button>
    <button class="btn btn-primary" type="submit" form="new-form" disabled={creating}>{#if creating}<span class="spinner" aria-hidden="true"></span>{t('adminUsers.creating')}{:else}<Icon name="plus" />{t('adminUsers.createUser')}{/if}</button>
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
