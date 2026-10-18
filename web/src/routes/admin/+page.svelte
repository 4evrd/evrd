<script>
  import { onMount } from 'svelte'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import ErrorSummary from '$lib/components/ErrorSummary.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast } from '$lib/ui.svelte.js'
  import { ago, limit, cap } from '$lib/format.js'
  import { usernameError, passwordError, onBlurCheck } from '$lib/validate.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  let ov = $state(null)
  let users = $state(null)
  let err = $state('')
  let q = $state('')
  let filter = $state('all')

  async function load() {
    try { [ov, users] = await Promise.all([api.admin.overview(), api.admin.users()]); err = '' } catch (e) { err = e.message }
  }
  onMount(() => { if (isAdmin) load() })

  const FILTERS = [['all', 'All'], ['admin', 'Admins'], ['suspended', 'Suspended'], ['sso', 'SSO']]
  const shown = $derived((users || []).filter((u) => {
    if (filter === 'admin' && u.role !== 'admin') return false
    if (filter === 'suspended' && !u.suspended) return false
    if (filter === 'sso' && !u.sso) return false
    const s = q.trim().toLowerCase()
    return !s || u.username.includes(s) || (u.email || '').toLowerCase().includes(s)
  }))

  // --- create ---
  let nu = $state({ username: '', email: '', password: '', role: 'user' })
  let ue = $state(''), pe = $state(''), formErr = $state('')
  let summary = $state([])
  let creating = $state(false)
  let userIn = $state(), passIn = $state()
  const vUser = () => (ue = usernameError(nu.username.trim().toLowerCase()))
  const vPass = () => (pe = passwordError(nu.password))
  async function create(e) {
    e.preventDefault()
    formErr = ''
    vUser(); vPass()
    summary = [['nu-name', ue], ['nu-pass', pe]].filter(([, m]) => m)
    if (summary.length) { (ue ? userIn : passIn).focus(); return }
    creating = true
    try {
      const u = await api.admin.createUser({ ...nu, username: nu.username.trim().toLowerCase() })
      toast(`${u.username} created`)
      nu = { username: '', email: '', password: '', role: 'user' }
      await load()
    } catch (err2) {
      if (err2.status === 409 || /username/i.test(err2.message)) { ue = cap(err2.message); userIn.focus() }
      else formErr = cap(err2.message)
    } finally { creating = false }
  }
</script>

<svelte:head><title>Users · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout err enter" role="alert"><Icon name="lock" /><div class="body"><p><b>Admins only.</b> Your account doesn’t have access to user management.</p></div></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>Users</h1>
      <p>Everyone with an evrd account: their role, limits and status. Server defaults apply unless you override them per user.</p>
    </div>
    <a class="btn btn-primary" href="#new-user" onclick={(e) => { e.preventDefault(); document.getElementById('nu-name')?.focus() }}><Icon name="plus" />New user</a>
  </div>

  <div class="tiles">
    <div class="tile">
      <div class="tile-top"><span class="tile-label"><Icon name="users" />Users</span></div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n" data-testid="tile-users">{ov ? ov.users : '–'}</span><span class="u">{ov ? `${ov.admins} admin${ov.admins === 1 ? '' : 's'} · ${ov.sso} via SSO` : 'accounts'}</span></div>
        <span class="tile-ic blue"><Icon name="user" /></span>
      </div>
    </div>
    <div class="tile">
      <div class="tile-top"><span class="tile-label"><Icon name="globe" />Networks</span></div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n">{ov ? ov.connectedNetworks : '–'}<span class="unit">/ {ov ? ov.networks : '–'}</span></span><span class="u">connected right now</span></div>
        <span class="tile-ic ok"><Icon name="activity" /></span>
      </div>
    </div>
    <div class="tile">
      <div class="tile-top"><span class="tile-label"><Icon name="plug" />Clients</span></div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n">{ov ? ov.attachedClients : '–'}</span><span class="u">IRC clients attached</span></div>
        <span class="tile-ic"><Icon name="monitor" /></span>
      </div>
    </div>
    <div class="tile">
      <div class="tile-top"><span class="tile-label"><Icon name="ban" />Suspended</span>{#if ov?.suspended}<button class="link-btn" type="button" onclick={() => (filter = 'suspended')}>Show</button>{/if}</div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n">{ov ? ov.suspended : '–'}</span><span class="u">accounts</span></div>
        <span class="tile-ic {ov?.suspended ? 'warn' : ''}"><Icon name="lock" /></span>
      </div>
    </div>
  </div>

  <section class="card enter" aria-labelledby="users-h">
    <div class="section-head">
      <h2 id="users-h"><Icon name="users" />Accounts</h2>
      {#if ov}<span class="chip" title="Server defaults">Defaults: {limit(ov.defaults.maxNetworks)} networks · {limit(ov.defaults.maxClients)} clients · {limit(ov.defaults.backlogLines)} lines</span>{/if}
    </div>
    <div class="ptools">
      <div class="search"><Icon name="search" /><input class="input" type="search" placeholder="Search username or email" aria-label="Search users" bind:value={q} /></div>
      <div class="tabs" role="group" aria-label="Filter">
        {#each FILTERS as [k, l]}<button class="tab" type="button" aria-pressed={filter === k} onclick={() => (filter = k)}>{l}</button>{/each}
      </div>
    </div>
    <div class="rows">
      {#if err}
        <p class="desc">Couldn’t load users: {err}</p>
      {:else if !users}
        <div class="skel"></div><div class="skel"></div><div class="skel"></div>
      {:else if !shown.length}
        <div class="empty"><span class="ic"><Icon name="search" /></span><h4>No users match</h4><p>Try a different search or filter.</p></div>
      {:else}
        <div class="row-head" aria-hidden="true" style="--cols:minmax(0,1fr) 7rem 8rem 11rem"><span>User</span><span>Networks</span><span>Last sign-in</span><span></span></div>
        {#each shown as u (u.id)}
          <a class="row user-row" href="/admin/users/{u.id}" style="--cols:minmax(0,1fr) 7rem 8rem 11rem" data-testid="user-row">
            <span class="main">
              <span class="name">{u.username}</span>
              <span class="sub">{u.email || 'no email'}</span>
            </span>
            <span class="cell">{u.networks}{u.clients ? ` · ${u.clients} attached` : ''}</span>
            <span class="cell">{ago(u.lastLoginAt)}</span>
            <span class="meta">
              {#if u.suspended}<span class="chip bad"><Icon name="ban" />Suspended</span>{/if}
              {#if u.role === 'admin'}<span class="chip ok"><Icon name="crown" />Admin</span>{/if}
              {#if u.sso}<span class="tag">SSO</span>{/if}
              <Icon name="chevron" class="go" />
            </span>
          </a>
        {/each}
      {/if}
    </div>
  </section>

  <section class="card enter" aria-labelledby="nu-h" id="new-user">
    <h3 id="nu-h"><Icon name="plus" />New user</h3>
    <p class="desc">{session.config.sso ? `People who sign in with ${session.config.ssoLabel} get an account automatically — create one here for someone who’ll use a password instead.` : 'They sign in with this username and password, and can use the same password for IRC clients.'}</p>
    <ErrorSummary errs={summary} />
    {#if formErr}<div class="callout err" role="alert"><Icon name="alert" /><div class="body"><p>{formErr}</p></div></div>{/if}
    <form class="form" novalidate onsubmit={create}>
      <div class="nu-grid">
        <Field id="nu-name" label="Username" required err={ue}>
          <input id="nu-name" class="input" autocapitalize="none" spellcheck="false" autocomplete="off" bind:value={nu.username} bind:this={userIn} aria-invalid={ue ? 'true' : undefined} aria-describedby="nu-name-err" onblur={onBlurCheck(() => nu.username && vUser())} />
        </Field>
        <Field id="nu-email" label="Email">
          <input id="nu-email" class="input" type="email" autocomplete="off" bind:value={nu.email} />
        </Field>
        <Field id="nu-pass" label="Password" required help="At least 8 characters. Share it with them privately." err={pe}>
          <input id="nu-pass" class="input" type="text" autocomplete="new-password" spellcheck="false" bind:value={nu.password} bind:this={passIn} aria-invalid={pe ? 'true' : undefined} aria-describedby="nu-pass-help nu-pass-err" onblur={onBlurCheck(() => nu.password && vPass())} />
        </Field>
        <Field id="nu-role" label="Role">
          <select id="nu-role" class="input" bind:value={nu.role}><option value="user">User</option><option value="admin">Admin</option></select>
        </Field>
      </div>
      <div><button class="btn btn-primary" type="submit" disabled={creating}>
        {#if creating}<span class="spin" aria-hidden="true"></span>Creating…{:else}Create user{/if}
      </button></div>
    </form>
  </section>
{/if}

<style>
  .user-row { color: inherit; text-decoration: none; }
  .user-row:hover .name { color: var(--link); }
  .user-row :global(.go) { color: var(--faint); }
  .cell { font-size: var(--fs-sm); color: var(--muted); white-space: nowrap; }
  .chip :global(svg.i) { width: 14px; height: 14px; }
  .nu-grid { display: grid; gap: var(--s4); grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 768px) { .nu-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 639px) { .cell { display: none; } }
</style>
