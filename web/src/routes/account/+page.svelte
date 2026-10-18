<script>
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import ErrorSummary from '$lib/components/ErrorSummary.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe, logout } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { date, ago, device, cap } from '$lib/format.js'
  import { passwordError, nickError, onBlurCheck } from '$lib/validate.js'

  const me = $derived(session.me)

  // --- profile ---
  let email = $state(session.me.email)
  let nick = $state(session.me.defaultNick)
  let realname = $state(session.me.defaultRealname)
  let emailErr = $state(''), nickErr = $state('')
  let savingProfile = $state(false)
  async function saveProfile(e) {
    e.preventDefault()
    emailErr = !me.sso && email.trim() && !email.includes('@') ? 'That doesn’t look like an email address.' : ''
    nickErr = nickError(nick.trim(), { optional: true })
    if (emailErr || nickErr) return
    savingProfile = true
    try {
      const patch = { defaultNick: nick.trim(), defaultRealname: realname.trim() }
      if (!me.sso) patch.email = email.trim()
      session.me = await api.updateMe(patch)
      toast('Profile saved')
    } catch (err) { nickErr = err.message } finally { savingProfile = false }
  }

  // --- password ---
  let cur = $state(''), next = $state(''), conf = $state('')
  let curErr = $state(''), nextErr = $state(''), confErr = $state('')
  let summary = $state([])
  let pwBusy = $state(false)
  let show = $state({ cur: false, next: false })
  let curIn = $state(), nextIn = $state(), confIn = $state()
  const vCur = () => (curErr = cur ? '' : 'Enter your current password.')
  const vNext = () => (nextErr = passwordError(next) || (next === cur ? 'Pick something different from your current password.' : ''))
  const vConf = () => (confErr = !conf ? 'Repeat the new password.' : conf !== next ? 'The new passwords don’t match.' : '')
  async function changePassword(e) {
    e.preventDefault()
    vCur(); vNext(); vConf()
    const errs = [['pw-current', curErr], ['pw-next', nextErr], ['pw-confirm', confErr]].filter(([, m]) => m)
    summary = errs
    if (errs.length) { ({ 'pw-current': curIn, 'pw-next': nextIn, 'pw-confirm': confIn })[errs[0][0]].focus(); return }
    pwBusy = true
    try {
      await api.changePassword(cur, next)
      cur = ''; next = ''; conf = ''
      toast('Password changed — other sessions were signed out')
      loadSessions()
    } catch (err) {
      if (err.status === 422 && /current/i.test(err.message)) { curErr = 'That isn’t your current password.'; curIn.focus() }
      else { nextErr = cap(err.message); nextIn.focus() }
    } finally { pwBusy = false }
  }

  // --- sessions ---
  let sessions = $state(null)
  let revoking = $state('')
  async function loadSessions() {
    try { sessions = await api.sessions() } catch { sessions = [] }
  }
  onMount(loadSessions)
  async function revoke(s) {
    revoking = s.id
    try { await api.revokeSession(s.id); await loadSessions(); toast('Session signed out') }
    catch (err) { toast(err.message, 'err') } finally { revoking = '' }
  }
  async function revokeOthers() {
    revoking = '*'
    try { await api.revokeOtherSessions(); await loadSessions(); toast('Signed out everywhere else') }
    catch (err) { toast(err.message, 'err') } finally { revoking = '' }
  }
  const others = $derived(sessions ? sessions.filter((s) => !s.current).length : 0)

  // --- delete ---
  async function deleteAccount() {
    const ok = await confirmDialog({
      title: 'Delete your account?',
      body: `<p>This disconnects every network, deletes your saved history, networks and client passwords, and signs you out. It can’t be undone.</p>`,
      confirmLabel: 'Delete my account', danger: true, requireText: me.username,
    })
    if (!ok) return
    try {
      await api.deleteMe(me.username)
      location.href = '/'
    } catch (err) { toast(err.message, 'err') }
  }
</script>

<svelte:head><title>Account · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Account</h1>
    <p>Your evrd account — how you sign in, the defaults for new networks, and where you’re signed in.</p>
  </div>
  <button class="btn" type="button" onclick={logout}><Icon name="logout" />Sign out</button>
</div>

<div class="grid-2 enter">
  <section class="card frost connect" aria-labelledby="id-h">
    <h3 id="id-h"><Icon name="user" />Identity</h3>
    <div class="kv-list">
      <div class="kv"><div class="text"><span class="k">Username</span><span class="v" data-testid="acct-username">{me.username}</span></div></div>
      <div class="kv"><div class="text"><span class="k">Role</span><span class="v">{me.role === 'admin' ? 'Admin' : 'User'}</span></div></div>
      <div class="kv"><div class="text"><span class="k">Signs in with</span><span class="v">{me.sso ? session.config.ssoLabel : 'Username & password'}{me.sso && me.hasPassword ? ' or password' : ''}</span></div></div>
      <div class="kv"><div class="text"><span class="k">Member since</span><span class="v">{date(me.createdAt)}</span></div></div>
    </div>
    <p class="desc">Your username is also your bouncer login, so it can’t be changed.</p>
  </section>

  <section class="card" aria-labelledby="prof-h">
    <h3 id="prof-h"><Icon name="settings" />Profile &amp; defaults</h3>
    <form class="form" novalidate onsubmit={saveProfile}>
      <Field id="p-email" label="Email" help={me.sso ? `Managed by ${session.config.ssoLabel}.` : 'For admins to reach you. Optional.'} err={emailErr}>
        <input id="p-email" class="input" type="email" autocomplete="email" bind:value={email} readonly={me.sso} aria-describedby="p-email-help p-email-err" />
      </Field>
      <Field id="p-nick" label="Default nick" help="Used when a new network doesn’t set one." err={nickErr}>
        <input id="p-nick" class="input" autocapitalize="none" spellcheck="false" bind:value={nick} aria-describedby="p-nick-help p-nick-err" onblur={onBlurCheck(() => nick && (nickErr = nickError(nick.trim(), { optional: true })))} />
      </Field>
      <Field id="p-real" label="Default real name" help="Shown in /whois on new networks.">
        <input id="p-real" class="input" bind:value={realname} aria-describedby="p-real-help" />
      </Field>
      <div><button class="btn btn-primary" type="submit" disabled={savingProfile}>
        {#if savingProfile}<span class="spin" aria-hidden="true"></span>Saving…{:else}Save{/if}
      </button></div>
    </form>
  </section>
</div>

<div class="grid-2 enter">
  <section class="card" aria-labelledby="pw-h">
    <h3 id="pw-h"><Icon name="lock" />Password</h3>
    {#if !me.hasPassword}
      <p class="desc">You sign in with {session.config.ssoLabel}, so your password is managed there. IRC clients use <a href="/clients">client passwords</a>.</p>
    {:else}
      <p class="desc">Used to sign in here and by IRC clients that send it. Changing it signs you out everywhere else.</p>
      <ErrorSummary errs={summary} />
      <form class="form" novalidate autocomplete="on" onsubmit={changePassword}>
        <input type="text" name="username" autocomplete="username" value={me.username} hidden readonly />
        <Field id="pw-current" label="Current password" required err={curErr}>
          <div class="input-reveal">
            <input id="pw-current" class="input" type={show.cur ? 'text' : 'password'} autocomplete="current-password" required aria-describedby="pw-current-err" aria-invalid={curErr ? 'true' : undefined} bind:value={cur} bind:this={curIn} oninput={() => curErr && vCur()} />
            <button class="reveal-btn" type="button" aria-pressed={show.cur} aria-label="Show current password" onclick={() => (show.cur = !show.cur)}>{show.cur ? 'Hide' : 'Show'}</button>
          </div>
        </Field>
        <Field id="pw-next" label="New password" required help="At least 8 characters. A passphrase works well." err={nextErr}>
          <div class="input-reveal">
            <input id="pw-next" class="input" type={show.next ? 'text' : 'password'} autocomplete="new-password" required aria-describedby="pw-next-help pw-next-err" aria-invalid={nextErr ? 'true' : undefined} bind:value={next} bind:this={nextIn} onblur={onBlurCheck(() => next && vNext())} oninput={() => nextErr && vNext()} />
            <button class="reveal-btn" type="button" aria-pressed={show.next} aria-label="Show new password" onclick={() => (show.next = !show.next)}>{show.next ? 'Hide' : 'Show'}</button>
          </div>
        </Field>
        <Field id="pw-confirm" label="Repeat new password" required err={confErr}>
          <input id="pw-confirm" class="input" type="password" autocomplete="new-password" required aria-describedby="pw-confirm-err" aria-invalid={confErr ? 'true' : undefined} bind:value={conf} bind:this={confIn} onblur={onBlurCheck(() => conf && vConf())} oninput={() => confErr && vConf()} />
        </Field>
        <div><button class="btn btn-primary" type="submit" disabled={pwBusy}>
          {#if pwBusy}<span class="spin" aria-hidden="true"></span>Changing…{:else}Change password{/if}
        </button></div>
      </form>
    {/if}
  </section>

  <section class="card" aria-labelledby="ses-h">
    <div class="card-head">
      <h3 id="ses-h"><Icon name="monitor" />Where you’re signed in</h3>
      {#if others}<button class="btn btn-sm" type="button" disabled={!!revoking} onclick={revokeOthers}>Sign out the other {others}</button>{/if}
    </div>
    <div class="rows">
      {#if !sessions}
        <div class="skel"></div>
      {:else}
        {#each sessions as s (s.id)}
          <div class="row" class:removing={revoking === s.id} style="--cols:minmax(0,1fr) auto">
            <span class="main">
              <span class="name">{device(s.userAgent)}{#if s.current}&nbsp;<span class="tag">this device</span>{/if}</span>
              <span class="sub">{s.ip || 'unknown IP'} · {s.method === 'oidc' ? session.config.ssoLabel : 'password'} · active {ago(s.lastSeenAt)}</span>
            </span>
            <span class="meta">{#if !s.current}<button class="btn btn-sm" type="button" disabled={!!revoking} onclick={() => revoke(s)}>Sign out</button>{/if}</span>
          </div>
        {/each}
      {/if}
    </div>
  </section>
</div>

<section class="card danger enter" aria-labelledby="del-h">
  <div class="section-head">
    <div>
      <h2 id="del-h"><Icon name="trash" />Delete account</h2>
      <p class="desc" style="margin-top:6px">Disconnects every network and deletes your history, networks and client passwords.{me.sso ? ` Your ${session.config.ssoLabel} sign-in itself isn’t affected.` : ''}</p>
    </div>
    <button class="btn btn-danger" type="button" onclick={deleteAccount}>Delete account</button>
  </div>
</section>
