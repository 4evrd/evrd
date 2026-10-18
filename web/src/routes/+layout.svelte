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
  import { session, load, logout } from '$lib/session.svelte.js'

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
  <div class="loading-shell" aria-busy="true"><span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>Loading…</span></span></span></div>
{:else if session.phase === 'signedOut'}
  <Login />
{:else if session.phase === 'error'}
  <div class="solo">
    <span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></span>
    <div class="callout bad" role="alert" id="load-error">
      <Icon name="alert" />
      <div class="od-stack">
        <span><b>Couldn’t reach the bouncer.</b> Your IRC connections are probably fine — the web app just can’t talk to the daemon right now.</span>
        <div><button class="btn btn-sm" type="button" onclick={retry} disabled={retrying}>{#if retrying}<span class="spinner" aria-hidden="true"></span>Retrying…{:else}<Icon name="refresh" />Try again{/if}</button></div>
      </div>
    </div>
  </div>
{:else if session.me?.mustChangePassword}
  <div class="solo" style="max-width:720px">
    <span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></span>
    <main id="main" class="card enter" style="display:grid;gap:var(--s5)">
      <div class="od-stack"><span class="caps">One more step</span><h1>Choose your own password</h1>
        <p class="desc">You signed in with a temporary password from an admin. Pick one only you know to continue.</p></div>
      <ChangePassword username={session.me.username} />
      <div><button class="btn btn-ghost btn-sm" type="button" onclick={logout}><Icon name="logout" />Sign out</button></div>
    </main>
  </div>
{:else}
  <Shell {flush}>{@render children()}</Shell>
{/if}

<Toasts />
<ConfirmDialog />
