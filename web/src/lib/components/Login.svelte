<script>
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import { api } from '$lib/api.js'
  import { session, load } from '$lib/session.svelte.js'

  const cfg = $derived(session.config || {})

  // Why the provider sent the user back without signing them in.
  const SSO_ERRORS = {
    taken: 'There’s already an evrd account with your username that isn’t linked to your sign-in. Ask an admin to link it.',
    no_account: 'Your sign-in worked, but there’s no evrd account for you yet. Ask an admin to create one.',
    suspended: 'This account is suspended. Ask an admin if you think that’s a mistake.',
    bad_username: 'Your username can’t be used for IRC (2–32 characters: a–z, 0–9, - and _). Change it with your sign-in provider, then try again.',
    expired: 'That sign-in took too long or was already used. Try again.',
    cancelled: 'Sign-in was cancelled.',
    sso_off: 'Single sign-on isn’t set up on this server.',
    host_denied: 'Signing in from your current address isn’t allowed for this account.',
  }
  const code = new URLSearchParams(location.search).get('login_error')
  let ssoErr = $state(code ? SSO_ERRORS[code] || 'Signing in didn’t work. Try again in a moment.' : '')
  if (code) history.replaceState(null, '', location.pathname)

  let leaving = $state(false)
  let username = $state(''), password = $state('')
  let err = $state('')
  let busy = $state(false)
  let userIn = $state()

  async function submit(e) {
    e.preventDefault()
    err = ''
    if (!username || !password) { err = 'Enter your username and password.'; userIn.focus(); return }
    busy = true
    try {
      await api.passwordLogin(username, password)
      await load()
    } catch (e2) {
      err = e2.message.charAt(0).toUpperCase() + e2.message.slice(1) + '.'
      password = ''
    } finally { busy = false }
  }
</script>

<div class="solo">
  <a class="brand" href="/"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></a>
  <main id="main" class="signin-grid">
    <section class="card signin-card card-sheen enter" aria-labelledby="si-h">
      <span class="chip ok live" style="justify-self:start"><span class="dot" aria-hidden="true"></span>Always connected</span>
      <h1 id="si-h">Stay on IRC, forever.</h1>
      <p class="lead">evrd keeps your networks connected while you’re away and replays what you missed when any client — web, irssi, WeeChat, HexChat — attaches.</p>

      {#if ssoErr}<div class="callout bad" role="alert"><Icon name="alert" /><span>{ssoErr}</span></div>{/if}

      {#if cfg.sso}
        <div class="od-cluster">
          <a class="btn btn-primary" id="sso-btn" href="/auth/login" data-sveltekit-reload onclick={() => (leaving = true)}>
            {#if leaving}<span class="spinner" aria-hidden="true"></span>Redirecting…{:else}Continue with {cfg.ssoLabel}<Icon name="chevron" />{/if}
          </a>
        </div>
      {/if}
      {#if cfg.sso && cfg.passwordLogin}<div class="or" aria-hidden="true">or with a password</div>{/if}

      {#if cfg.passwordLogin}
        <form class="form" novalidate onsubmit={submit} aria-label="Sign in with a password">
          {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{err}</span></div>{/if}
          <Field id="si-user" label="Username">
            <input id="si-user" class="input mono" autocomplete="username" autocapitalize="none" spellcheck="false" bind:value={username} bind:this={userIn} />
          </Field>
          <Field id="si-pass" label="Password">
            <input id="si-pass" class="input" type="password" autocomplete="current-password" bind:value={password} />
          </Field>
          <div><button class="btn {cfg.sso ? '' : 'btn-primary'}" type="submit" disabled={busy}>
            {#if busy}<span class="spinner" aria-hidden="true"></span>Signing in…{:else}Sign in{/if}
          </button></div>
        </form>
      {/if}
      {#if !cfg.sso && !cfg.passwordLogin}<p class="desc">Sign-in isn’t configured on this server yet.</p>{/if}
    </section>

    <section class="card enter" aria-hidden="true">
      <div class="section-head"><div class="od-stack"><span class="caps">#ircforever</span><h2>While you were away</h2></div><span class="count hot">3</span></div>
      <div class="log" style="padding:0">
        <div class="day-sep backlog">Missed while away · 3 lines</div>
        <div class="msg"><time>03:12</time><span class="nick" style="--c:var(--nick)">mira</span><span class="text">anyone around for the release?</span></div>
        <div class="msg"><time>03:40</time><span class="nick" style="--c:var(--warn)">tomas</span><span class="text">tagged v2.1 — notes in the topic</span></div>
        <div class="msg highlight"><time>07:05</time><span class="nick" style="--c:var(--nick)">mira</span><span class="text">you around? the bouncer kept your seat</span></div>
        <div class="msg event"><time>07:06</time><span class="nick">→</span><span class="text">you attached from HexChat</span></div>
      </div>
    </section>
  </main>
</div>
