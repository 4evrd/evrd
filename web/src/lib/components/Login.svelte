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

<div class="solo signin">
  <div class="solo-bar">
    <span class="brand">
      <span class="brand-mark" aria-hidden="true">#</span>
      <span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span>
    </span>
  </div>
  <main id="main" class="signin-grid">
    <section class="signin-card glass enter" aria-labelledby="si-h">
      <span class="chip ok live" style="justify-self:start"><span class="dot" aria-hidden="true"></span>Always connected</span>
      <h1 id="si-h">Stay on IRC, forever.</h1>
      <p class="lead">evrd keeps your networks connected while you’re away and replays what you missed when any client — web, irssi, WeeChat, HexChat — attaches.</p>

      {#if ssoErr}
        <div class="callout err" role="alert" style="justify-self:stretch"><Icon name="alert" /><div class="body"><p>{ssoErr}</p></div></div>
      {/if}

      {#if cfg.sso}
        <div class="od-cluster">
          <a class="btn btn-primary" id="sso-btn" href="/auth/login" data-sveltekit-reload onclick={() => (leaving = true)}>
            {#if leaving}<span class="spin" aria-hidden="true"></span>Redirecting…{:else}Continue with {cfg.ssoLabel}<Icon name="chevron" />{/if}
          </a>
        </div>
      {/if}

      {#if cfg.sso && cfg.passwordLogin}
        <div class="or" aria-hidden="true"><span>or with a password</span></div>
      {/if}

      {#if cfg.passwordLogin}
        <form class="form pw" novalidate onsubmit={submit} aria-label="Sign in with a password">
          {#if err}<div class="callout err" role="alert"><Icon name="alert" /><div class="body"><p>{err}</p></div></div>{/if}
          <Field id="si-user" label="Username">
            <input id="si-user" class="input" autocomplete="username" autocapitalize="none" spellcheck="false" bind:value={username} bind:this={userIn} />
          </Field>
          <Field id="si-pass" label="Password">
            <input id="si-pass" class="input" type="password" autocomplete="current-password" bind:value={password} />
          </Field>
          <div><button class="btn {cfg.sso ? '' : 'btn-primary'}" type="submit" disabled={busy}>
            {#if busy}<span class="spin" aria-hidden="true"></span>Signing in…{:else}Sign in{/if}
          </button></div>
        </form>
      {/if}

      {#if !cfg.sso && !cfg.passwordLogin}
        <p class="desc">Sign-in isn’t configured on this server yet.</p>
      {/if}
    </section>
    <section class="console enter" aria-hidden="true">
      <div class="console-bar"><span class="lights"><i></i><i></i><i></i></span><span>#ircforever — attached via evrd</span></div>
      <div class="console-body">
        <div class="line sys">-evrd- replaying 3 lines you missed</div>
        <div class="line"><span class="cmt">03:12</span> <span class="nick">&lt;mira&gt;</span> anyone around for the release?</div>
        <div class="line"><span class="cmt">03:40</span> <span class="nick">&lt;tomas&gt;</span> tagged v2.1 — notes in the topic</div>
        <div class="line"><span class="cmt">07:05</span> <span class="nick">&lt;mira&gt;</span> morning! you around?</div>
        <div class="line ok">-evrd- you’re caught up</div>
        <div class="line"><span class="prompt">&gt;</span> <span class="cursor"></span></div>
      </div>
    </section>
  </main>
</div>

<style>
  /* One centred column: the sign-in card, then an example session under it. */
  .signin { min-height: 100vh; display: grid; grid-template-rows: auto 1fr; max-width: 760px; }
  .signin-grid { display: grid; gap: var(--s5); align-content: center; grid-template-columns: minmax(0, 1fr); }
  .signin-card { padding: clamp(24px, 4vw, 44px); border-radius: var(--r-panel); display: grid; gap: var(--s5); justify-items: start; }
  .signin-card h1 { font-size: clamp(32px, 4.4vw, 52px); letter-spacing: -0.03em; }
  .signin-card .lead { color: var(--muted); font-size: 17px; max-width: 52ch; }
  .signin-card .pw { justify-self: stretch; max-width: 420px; }
  .signin-card .desc { color: var(--muted); }
  .or { justify-self: stretch; max-width: 420px; display: flex; align-items: center; gap: var(--s3); color: var(--faint); font-size: var(--fs-sm); }
  .or::before, .or::after { content: ''; flex: 1; height: 1px; background: var(--line); }
  .signin-grid > .console { opacity: .9; }
  .signin-grid > .console .console-body { padding-block: var(--s4); }
  .nick { color: var(--nick); }
</style>
