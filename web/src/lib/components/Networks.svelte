<script>
  import { onMount, onDestroy } from 'svelte'
  import NetworkForm from './NetworkForm.svelte'

  let { networks: bloc, onopen } = $props()
  const { networks } = bloc
  let adding = $state(false)
  let timer

  onMount(() => {
    bloc.load()
    timer = setInterval(() => bloc.load(), 4000) // keep statuses fresh
  })
  onDestroy(() => clearInterval(timer))

  async function add(net) {
    await bloc.add(net)
    adding = false
  }
</script>

<div class="flex items-center justify-between">
  <h2 class="text-lg font-semibold">Networks</h2>
  <button
    class="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-bg"
    onclick={() => (adding = !adding)}
  >
    {adding ? 'Cancel' : '+ Add network'}
  </button>
</div>

{#if adding}
  <div class="mt-4 rounded-lg border border-border bg-surface p-4">
    <NetworkForm onsave={add} oncancel={() => (adding = false)} />
  </div>
{/if}

<div class="mt-4 space-y-2">
  {#each $networks as net (net.id)}
    <div
      class="flex items-center justify-between rounded-lg border border-border bg-surface px-4 py-3"
    >
      <button class="min-w-0 text-left" onclick={() => onopen(net)}>
        <div class="flex items-center gap-2">
          <span class="font-medium">{net.name}</span>
          <span
            class="rounded-full px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide {net.status ===
            'connected'
              ? 'bg-accent/15 text-accent'
              : net.status === 'connecting'
                ? 'bg-amber-500/15 text-amber-400'
                : 'bg-border text-muted'}"
          >
            {net.status}
          </span>
        </div>
        <div class="text-muted mt-0.5 truncate text-xs">
          {net.host}:{net.port}{net.tls ? ' · TLS' : ''} · {net.nick}
          {#if net.channels?.length}· {net.channels.join(' ')}{/if}
        </div>
      </button>
      <div class="flex shrink-0 items-center gap-2">
        <button
          class="rounded-md bg-accent/90 px-2 py-1 text-xs font-medium text-bg"
          onclick={() => onopen(net)}>Open</button
        >
        <button
          class="rounded-md border border-border px-2 py-1 text-xs hover:border-accent"
          onclick={() => bloc.toggle(net)}
        >
          {net.status === 'disconnected' ? 'Connect' : 'Disconnect'}
        </button>
        <button
          class="rounded-md border border-border px-2 py-1 text-xs text-red-400 hover:border-red-400"
          onclick={() => bloc.remove(net.id)}>Remove</button
        >
      </div>
    </div>
  {:else}
    <p class="text-muted rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm">
      No networks yet. Add one to get started.
    </p>
  {/each}
</div>
