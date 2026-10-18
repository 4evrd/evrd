<script>
  import '../app.css'
  import { onMount } from 'svelte'
  import Chrome from '$lib/components/Chrome.svelte'
  import Login from '$lib/components/Login.svelte'
  import Toasts from '$lib/components/Toasts.svelte'
  import ConfirmDialog from '$lib/components/ConfirmDialog.svelte'
  import Icon from '$lib/components/Icon.svelte'
  import { session, load } from '$lib/session.svelte.js'

  let { children } = $props()
  let retrying = $state(false)
  onMount(load)

  async function retry() {
    retrying = true
    await load()
    retrying = false
  }
</script>

<a class="skip" href="#main">Skip to content</a>

{#if session.phase === 'loading'}
  <div class="solo" aria-busy="true">
    <div class="solo-bar"><span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></span></div>
    <div class="skel"></div><div class="skel"></div>
  </div>
{:else if session.phase === 'signedOut'}
  <Login />
{:else if session.phase === 'error'}
  <div class="solo">
    <div class="solo-bar"><span class="brand"><span class="brand-mark" aria-hidden="true">#</span><span class="brand-text"><b>evrd</b><span>IRC bouncer</span></span></span></div>
    <div class="callout err enter" id="load-error" role="alert">
      <Icon name="alert" />
      <div class="body">
        <p><b>Couldn’t reach the bouncer.</b> Your IRC connections are probably fine — the web app just can’t talk to the daemon right now.</p>
        <div><button class="btn btn-sm" type="button" onclick={retry} disabled={retrying}>
          {#if retrying}<span class="spin" aria-hidden="true"></span>Retrying…{:else}<Icon name="refresh" />Try again{/if}
        </button></div>
      </div>
    </div>
  </div>
{:else}
  <Chrome>{@render children()}</Chrome>
{/if}

<Toasts />
<ConfirmDialog />
