<script>
  let { auth } = $props()
  const { error, busy } = auth

  let username = $state('')
  let password = $state('')

  function submit(e) {
    e.preventDefault()
    auth.login(username, password)
  }
</script>

<div class="grid min-h-dvh place-items-center p-6">
  <form
    onsubmit={submit}
    class="w-full max-w-sm rounded-lg border border-border bg-surface p-6"
  >
    <h1 class="text-accent text-2xl font-bold tracking-tight">evrd</h1>
    <p class="text-muted mt-1 text-sm">Sign in to your bouncer.</p>

    <label class="text-muted mt-5 block text-xs" for="u">Username</label>
    <input
      id="u"
      class="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
      bind:value={username}
      autocomplete="username"
      spellcheck="false"
    />

    <label class="text-muted mt-3 block text-xs" for="p">Password</label>
    <input
      id="p"
      type="password"
      class="mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
      bind:value={password}
      autocomplete="current-password"
    />

    {#if $error}
      <p class="mt-3 text-xs text-red-400">{$error}</p>
    {/if}

    <button
      class="mt-5 w-full rounded-md bg-accent px-3 py-2 text-sm font-medium text-bg disabled:opacity-50"
      disabled={$busy}
    >
      {$busy ? 'Signing in…' : 'Sign in'}
    </button>
  </form>
</div>
