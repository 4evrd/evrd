<script>
  import '../app.css'
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import Shell from '$lib/components/Shell.svelte'
  import Login from '$lib/components/Login.svelte'
  import Toasts from '$lib/components/Toasts.svelte'
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte'
  import ChangePassword from '$lib/components/ChangePassword.svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Msg from '$lib/components/Msg.svelte'
  import { session, load, logout } from '$lib/session.svelte.js'
  import { i18n, t } from '$lib/i18n.svelte.js'
  import { useI18n } from '$lib/format.js'

  // format.js is plain JS: give it the app's language
  useI18n(t, i18n)

  let { children } = $props()
  let retrying = $state(false)
  onMount(load)

  async function retry() {
    retrying = true
    await load()
    retrying = false
  }
  // the chat page fills the workspace without page padding
  const flush = $derived(page.url.pathname === '/chat')
</script>

{#if session.phase === 'loading'}
  <div class="loading-shell" aria-busy="true"><span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>{t('common.loading')}</span></span></span></div>
{:else if session.phase === 'signedOut'}
  <Login />
{:else if session.phase === 'error'}
  <div class="solo">
    <span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>{t('shell.tagline')}</span></span></span>
    <div class="callout bad" role="alert" id="load-error">
      <Icon name="alert" />
      <div class="od-stack">
        <span><Msg key="shell.loadError">{#snippet title()}<b>{t('shell.loadErrorTitle')}</b>{/snippet}</Msg></span>
        <div><button class="btn btn-sm" type="button" onclick={retry} disabled={retrying}>{#if retrying}<span class="spinner" aria-hidden="true"></span>{t('shell.retrying')}{:else}<Icon name="refresh" />{t('shell.tryAgain')}{/if}</button></div>
      </div>
    </div>
  </div>
{:else if session.me?.mustChangePassword}
  <div class="solo" style="max-width:720px">
    <span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>{t('shell.tagline')}</span></span></span>
    <main id="main" class="card enter" style="display:grid;gap:var(--s5)">
      <div class="od-stack"><span class="caps">{t('shell.oneMoreStep')}</span><h1>{t('shell.choosePassword')}</h1>
        <p class="desc">{t('shell.tempPassword')}</p></div>
      <ChangePassword username={session.me.username} />
      <div><button class="btn btn-ghost btn-sm" type="button" onclick={logout}><Icon name="logout" />{t('shell.signOut')}</button></div>
    </main>
  </div>
{:else}
  <Shell {flush}>{@render children()}</Shell>
{/if}

<Toasts />
<ConfirmDialog />
