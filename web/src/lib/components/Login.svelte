<script>
  import Icon from './Icon.svelte'
  import PasswordInput from './PasswordInput.svelte'
  import Field from './Field.svelte'
  import { api } from '$lib/api.js'
  import { session, load } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  const cfg = $derived(session.config || {})

  // Why the provider sent the user back without signing them in.
  const SSO_ERRORS = {
    taken: 'shell.ssoTaken',
    no_account: 'shell.ssoNoAccount',
    suspended: 'shell.ssoSuspended',
    bad_username: 'shell.ssoBadUsername',
    expired: 'shell.ssoExpired',
    cancelled: 'shell.ssoCancelled',
    sso_off: 'shell.ssoOff',
    host_denied: 'shell.ssoHostDenied',
  }
  const code = new URLSearchParams(location.search).get('login_error')
  // a message key, so the text follows the language
  let ssoErr = $state(code ? SSO_ERRORS[code] || 'shell.ssoFailed' : '')
  if (code) history.replaceState(null, '', location.pathname)

  let leaving = $state(false)
  let username = $state(''), password = $state('')
  let err = $state('')
  let busy = $state(false)
  let userIn = $state()

  async function submit(e) {
    e.preventDefault()
    err = ''
    if (!username || !password) { err = t('shell.enterCredentials'); userIn.focus(); return }
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
  <a class="brand" href="/"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>{t('shell.tagline')}</span></span></a>
  <main id="main" class="signin-grid">
    <section class="card signin-card card-sheen enter" aria-labelledby="si-h">
      <span class="chip ok live" style="justify-self:start"><span class="dot" aria-hidden="true"></span>{t('shell.alwaysConnected')}</span>
      <h1 id="si-h">{t('shell.headline')}</h1>
      <p class="lead">{t('shell.lead')}</p>

      {#if ssoErr}<div class="callout bad" role="alert"><Icon name="alert" /><span>{t(ssoErr)}</span></div>{/if}

      {#if cfg.sso}
        <div class="od-cluster">
          <a class="btn btn-primary" id="sso-btn" href={location.pathname === '/' ? '/auth/login' : '/auth/login?next=' + encodeURIComponent(location.pathname + location.search)} data-sveltekit-reload onclick={() => (leaving = true)}>
            {#if leaving}<span class="spinner" aria-hidden="true"></span>{t('shell.redirecting')}{:else}{t('shell.continueWith', { provider: cfg.ssoLabel })}<Icon name="chevron" />{/if}
          </a>
        </div>
      {/if}
      {#if cfg.sso && cfg.passwordLogin}<div class="or" aria-hidden="true">{t('shell.orPassword')}</div>{/if}

      {#if cfg.passwordLogin}
        <form class="form" novalidate onsubmit={submit} aria-label={t('shell.passwordForm')}>
          {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{err}</span></div>{/if}
          <Field id="si-user" label={t('shell.username')}>
            <input id="si-user" class="input mono" autocomplete="username" autocapitalize="none" spellcheck="false" bind:value={username} bind:this={userIn} />
          </Field>
          <Field id="si-pass" label={t('shell.password')}>
            <PasswordInput id="si-pass" autocomplete="current-password" bind:value={password} />
          </Field>
          <div><button class="btn {cfg.sso ? '' : 'btn-primary'}" type="submit" disabled={busy}>
            {#if busy}<span class="spinner" aria-hidden="true"></span>{t('shell.signingIn')}{:else}{t('shell.signIn')}{/if}
          </button></div>
        </form>
      {/if}
      {#if !cfg.sso && !cfg.passwordLogin}<p class="desc">{t('shell.noSignIn')}</p>{/if}
    </section>

    <section class="card enter" aria-hidden="true">
      <div class="section-head"><div class="od-stack"><span class="caps">#ircforever</span><h2>{t('shell.whileAway')}</h2></div><span class="count hot">3</span></div>
      <div class="log" style="padding:0">
        <div class="day-sep backlog">{t('shell.missedLines')}</div>
        <div class="msg"><time>03:12</time><span class="nick" style="--c:var(--nick)">mira</span><span class="text">{t('shell.sample1')}</span></div>
        <div class="msg"><time>03:40</time><span class="nick" style="--c:var(--warn)">tomas</span><span class="text">{t('shell.sample2')}</span></div>
        <div class="msg highlight"><time>07:05</time><span class="nick" style="--c:var(--nick)">mira</span><span class="text">{t('shell.sample3')}</span></div>
        <div class="msg event"><time>07:06</time><span class="nick">→</span><span class="text">{t('shell.sample4')}</span></div>
      </div>
    </section>
  </main>
</div>
