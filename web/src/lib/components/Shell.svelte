<script>
  import Networks from './Networks.svelte'
  import Console from './Console.svelte'

  let { auth, networks } = $props()
  const { session } = auth
  let openNet = $state(null)
</script>

<div class="min-h-dvh">
  <header
    class="flex items-center justify-between border-b border-border px-6 py-3"
  >
    <div class="flex items-baseline gap-2">
      <span class="text-accent font-bold">evrd</span>
      <span class="text-muted text-xs">bouncer</span>
    </div>
    <div class="text-muted flex items-center gap-3 text-xs">
      <span>{$session.user}</span>
      <button
        class="rounded-md border border-border px-2 py-1 hover:border-accent"
        onclick={() => auth.logout()}>sign out</button
      >
    </div>
  </header>

  <main class="mx-auto max-w-4xl p-6">
    {#if openNet}
      <Console network={openNet} onback={() => (openNet = null)} />
    {:else}
      <Networks {networks} onopen={(n) => (openNet = n)} />
    {/if}
  </main>
</div>
