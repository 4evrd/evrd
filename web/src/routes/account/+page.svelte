<script>
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import ChangePassword from '$lib/components/ChangePassword.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago } from '$lib/format.js'
  import { check } from '$lib/validate.js'

  const me = $derived(session.me)
  const TABS = [['profile', 'Profile'], ['security', 'Security'], ['sessions', 'Sessions'], ['danger', 'Delete account']]
  let tab = $state(TABS.some(([k]) => '#' + k === location.hash) ? location.hash.slice(1) : 'profile')
  function pick(k) { tab = k; history.replaceState(null, '', '#' + k) }
  function tabKey(e, i) {
    if (e.key === 'ArrowRight') { pick(TABS[(i + 1) % TABS.length][0]); document.getElementById('t-' + TABS[(i + 1) % TABS.length][0])?.focus() }
    if (e.key === 'ArrowLeft') { pick(TABS[(i - 1 + TABS.length) % TABS.length][0]); document.getElementById('t-' + TABS[(i - 1 + TABS.length) % TABS.length][0])?.focus() }
  }

  // profile
  let email = $state(session.me.email)
  let nick = $state(session.me.defaultNick)
  let alt = $state(session.me.defaultAltNick)
  let real = $state(session.me.defaultRealname)
  let away = $state(session.me.autoAway)
  let perr = $state({})
  let saving = $state(false)
  async function saveProfile(e) {
    e.preventDefault()
    perr = { email: me.sso ? '' : check('email', email), nick: check('nick', nick), alt: check('optnick', alt) }
    if (Object.values(perr).some(Boolean)) return
    saving = true
    try {
      const patch = { defaultNick: nick.trim(), defaultAltNick: alt.trim(), defaultRealname: real.trim(), autoAway: away }
      if (!me.sso) patch.email = email.trim()
      session.me = await api.updateMe(patch)
      toast('Profile saved')
    } catch (err) { toast(err.message, 'bad') } finally { saving = false }
  }

  // sessions
  let sessions = $state(null)
  async function loadSessions() { try { sessions = await api.sessions() } catch { sessions = [] } }
  onMount(loadSessions)
  const device = (ua) => {
    if (!ua) return 'Unknown device'
    const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
    const os = /iPhone|iPad/.test(ua) ? 'iPhone' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
    return os ? `${b} on ${os}` : b
  }
  async function revoke(s) {
    try { await api.revokeSession(s.id); await loadSessions(); toast(`Signed out ${device(s.userAgent)}`) } catch (e) { toast(e.message, 'bad') }
  }
  async function revokeOthers() {
    if (!(await confirmDialog({ title: 'Sign out other sessions?', body: 'Every browser except this one is signed out. IRC clients stay attached.', action: 'Sign out others' }))) return
    try { await api.revokeOtherSessions(); await loadSessions(); toast('Signed out other sessions') } catch (e) { toast(e.message, 'bad') }
  }
  const others = $derived(sessions ? sessions.filter((s) => !s.current).length : 0)


  async function deleteAccount() {
    if (!(await confirmDialog({ title: 'Delete your account?', body: `All ${me.usage.networks} network${me.usage.networks === 1 ? '' : 's'} disconnect, your client passwords are revoked and your history is deleted for good.`, action: 'Delete account', typed: me.username }))) return
    try { await api.deleteMe(me.username); location.href = '/' } catch (e) { toast(e.message, 'bad') }
  }
</script>

<svelte:head><title>Account · evrd</title></svelte:head>

<div class="settings" style="display:grid;gap:var(--s6)">
  <div class="page-head enter">
    <div class="titles">
      <h1>Account settings</h1>
      <p>Your profile, sign-in and the defaults new networks start with.</p>
    </div>
  </div>

  <div class="tabs" role="tablist" aria-label="Account sections">
    {#each TABS as [k, l], i}
      <button class="tab" role="tab" id="t-{k}" aria-selected={tab === k} aria-controls={k} tabindex={tab === k ? 0 : -1} onclick={() => pick(k)} onkeydown={(e) => tabKey(e, i)}>{l}</button>
    {/each}
  </div>

  {#if tab === 'profile'}
    <div role="tabpanel" id="profile" aria-labelledby="t-profile" class="card">
      <form class="form" novalidate onsubmit={saveProfile}>
        <div class="panel-grid">
          <div class="od-stack"><h2>Identity</h2><p class="desc">Your username is how clients attach. Ask an admin to change it.</p></div>
          <div class="form-grid">
            <Field id="p-user" label="Username" help="Locked · used in server passwords."><input class="input mono" id="p-user" value={me.username} readonly data-testid="acct-username" /></Field>
            <Field id="p-email" label="Email" help={me.sso ? `Managed by ${session.config.ssoLabel}.` : 'Optional. Lets an admin reach you.'} err={perr.email}>
              <input class="input" id="p-email" type="email" autocomplete="email" bind:value={email} readonly={me.sso} />
            </Field>
          </div>
        </div>
        <hr class="divider" />
        <div class="panel-grid">
          <div class="od-stack"><h2>Defaults for new networks</h2><p class="desc">Pre-filled when you add a network. Existing networks keep their own settings.</p></div>
          <div class="form-grid">
            <Field id="p-nick" label="Default nickname" required err={perr.nick}><input class="input mono" id="p-nick" spellcheck="false" bind:value={nick} /></Field>
            <Field id="p-alt" label="Alternate nickname" help="Used when the first one is taken." err={perr.alt}><input class="input mono" id="p-alt" spellcheck="false" bind:value={alt} /></Field>
            <Field id="p-real" label="Real name" help="Shown in /whois. Can be anything." span><input class="input" id="p-real" bind:value={real} /></Field>
            <div class="check-row span-2">
              <span class="od-field"><span id="away-l">Mark me away when no client is attached</span><span>evrd sets /away and clears it when you come back.</span></span>
              <Switch checked={away} labelledby="away-l" onchange={(v) => (away = v)} />
            </div>
          </div>
        </div>
        <div class="form-foot"><button class="btn btn-primary" type="submit" disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />Save profile{/if}</button></div>
      </form>
    </div>
  {:else if tab === 'security'}
    <div role="tabpanel" id="security" aria-labelledby="t-security" class="card">
      <div class="panel-grid">
        <div class="od-stack"><h2>Password</h2><p class="desc">{me.hasPassword ? 'Changing it doesn’t affect your client passwords. Other web sessions are signed out.' : `You sign in with ${session.config.ssoLabel}, so your password is managed there. IRC clients use client passwords.`}</p></div>
        {#if me.hasPassword}<ChangePassword username={me.username} />{:else}<div><a class="btn" href="/clients"><Icon name="key" />Client passwords</a></div>{/if}
      </div>
    </div>
  {:else if tab === 'sessions'}
    <div role="tabpanel" id="sessions" aria-labelledby="t-sessions" class="card">
      <div class="section-head">
        <div class="od-stack"><h2>Where you’re signed in</h2><p>Web sessions only. Manage IRC clients on the <a href="/clients">IRC clients</a> page.</p></div>
        <button class="btn btn-danger-ghost btn-sm" type="button" disabled={!others} onclick={revokeOthers}>Sign out other sessions</button>
      </div>
      <div class="rows">
        {#if !sessions}<div class="skel"></div>{:else}
          {#each sessions as s (s.id)}
            <div class="row sess-row">
              <span class="sic"><Icon name={/iPhone|Android/.test(s.userAgent || '') ? 'message' : 'monitor'} /></span>
              <span class="od-field"><span class="name">{device(s.userAgent)}</span><span class="sub">{s.ip || 'unknown address'} · {s.method === 'oidc' ? session.config.ssoLabel : 'password'} · {s.current ? 'This session' : 'Active ' + ago(s.lastSeenAt)}</span></span>
              {#if s.current}<span class="chip ok">This device</span>{:else}<button class="btn btn-ghost btn-sm" type="button" aria-label="Sign out {device(s.userAgent)}" onclick={() => revoke(s)}>Sign out</button>{/if}
            </div>
          {/each}
        {/if}
      </div>
    </div>
  {:else}
    <div role="tabpanel" id="danger" aria-labelledby="t-danger" class="card danger-card">
      <div class="panel-grid">
        <div class="od-stack"><h2>Delete account</h2><p class="desc">This disconnects every network, revokes all client passwords and deletes your chat history. It can’t be undone.</p></div>
        <div class="od-stack" style="--od-gap:16px;align-items:flex-start">
          {#if me.role === 'admin'}
            {#if me.otherAdmins?.length}<p class="desc" style="color:var(--fg)">Admin rights stay with {me.otherAdmins.length === 1 ? 'the other admin,' : 'the other admins:'} {#each me.otherAdmins as a, i}<span class="code">{a}</span>{i < me.otherAdmins.length - 1 ? ', ' : ''}{/each}.</p>
            {:else}<div class="callout warn"><Icon name="alert" /><span><b>You’re the only admin.</b> Make someone else an admin before deleting your account.</span></div>{/if}
          {/if}
          <button class="btn btn-danger" type="button" onclick={deleteAccount} disabled={me.role === 'admin' && !me.otherAdmins?.length}><Icon name="trash" />Delete my account</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .panel-grid { display: grid; grid-template-columns: minmax(0, 280px) minmax(0, 1fr); gap: var(--s7); }
  .panel-grid :global(.od-stack) { --od-gap: 6px; }
  :global(.form-foot) { display: flex; justify-content: flex-end; gap: var(--s2); }
  .sess-row { --cols: 36px minmax(0, 1fr) auto; }
  .sic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--muted); }
  .danger-card { border-color: oklch(68% 0.18 27 / 0.35); background: linear-gradient(180deg, var(--glass), oklch(68% 0.18 27 / 0.06)); }
  @media (max-width: 959px) { .panel-grid { grid-template-columns: minmax(0, 1fr); gap: var(--s4); } }
</style>
