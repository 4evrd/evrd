<script>
  import { createConnectionBloc } from './lib/blocs/connection.bloc.js'

  const conn = createConnectionBloc()
  const { status, log } = conn

  // Default to the evrd WebSocket listener; overridable in the field.
  let url = $state('ws://localhost:8097')
</script>

<main class="min-h-dvh flex flex-col items-center justify-center gap-6 p-6">
  <header class="text-center">
    <h1 class="text-accent text-3xl font-bold tracking-tight">evrd</h1>
    <p class="text-muted mt-1 text-sm">IRCForever bouncer — web client</p>
  </header>

  <section
    class="w-full max-w-xl rounded-lg border border-border bg-surface p-4"
  >
    <div class="flex items-center gap-2">
      <input
        class="flex-1 rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
        bind:value={url}
        spellcheck="false"
        autocomplete="off"
      />
      {#if $status === 'open'}
        <button
          class="rounded-md border border-border px-3 py-2 text-sm hover:border-accent"
          onclick={() => conn.disconnect()}>disconnect</button
        >
      {:else}
        <button
          class="rounded-md bg-accent px-3 py-2 text-sm font-medium text-bg"
          onclick={() => conn.connect(url)}>connect</button
        >
      {/if}
    </div>

    <p class="text-muted mt-2 text-xs">status: {$status}</p>

    <pre
      class="mt-3 max-h-64 overflow-auto rounded-md border border-border bg-bg p-3 text-xs leading-relaxed">{#each $log as line}{line}
{/each}</pre>
  </section>

  <p class="text-muted text-xs">scaffold — the full client UI lands here.</p>
</main>
