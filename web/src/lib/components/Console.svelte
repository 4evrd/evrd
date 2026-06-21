<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { api } from '../api/client.js'

  let { network, onback } = $props()

  let buffers = $state({}) // target -> message[]
  let target = $state(network.channels?.[0] || network.name)
  let text = $state('')
  let logEl
  let stream = null

  function ensure(t) {
    if (!buffers[t]) buffers[t] = []
  }

  async function scrollDown() {
    await tick()
    if (logEl) logEl.scrollTop = logEl.scrollHeight
  }

  async function loadHistory(t) {
    ensure(t)
    buffers[t] = (await api.messages(network.id, t)) ?? []
    await scrollDown()
  }

  async function select(t) {
    target = t
    if (!buffers[t] || buffers[t].length === 0) await loadHistory(t)
    else await scrollDown()
  }

  function onMsg(m) {
    ensure(m.target)
    buffers[m.target] = [...buffers[m.target], m]
    if (m.target === target) scrollDown()
  }

  function send() {
    const t = text.trim()
    if (!t) return
    stream?.send(target, t)
    text = ''
  }

  function onKey(e) {
    if (e.key === 'Enter') {
      e.preventDefault()
      send()
    }
  }

  function fmtTime(unix) {
    const d = new Date(unix * 1000)
    return d.toTimeString().slice(0, 5)
  }

  onMount(async () => {
    if (network.status === 'disconnected') {
      try {
        await api.connect(network.id)
      } catch {
        // ignore; stream open will surface failures
      }
    }
    for (const ch of network.channels || []) ensure(ch)
    await loadHistory(target)
    stream = api.openStream(network.id, onMsg)
  })
  onDestroy(() => stream?.close())

  const targets = $derived([...new Set([...(network.channels || []), ...Object.keys(buffers)])])
</script>

<div class="flex h-[calc(100dvh-7rem)] gap-3">
  <!-- buffer list -->
  <aside class="w-44 shrink-0 rounded-lg border border-border bg-surface p-2">
    <button
      class="text-muted mb-2 w-full rounded-md border border-border px-2 py-1 text-xs hover:border-accent"
      onclick={onback}>← networks</button
    >
    <div class="text-muted px-2 pb-1 text-[10px] uppercase tracking-wide">{network.name}</div>
    {#each targets as t}
      <button
        class="block w-full truncate rounded-md px-2 py-1 text-left text-sm {t === target
          ? 'bg-accent/15 text-accent'
          : 'hover:bg-border/50'}"
        onclick={() => select(t)}>{t}</button
      >
    {/each}
  </aside>

  <!-- conversation -->
  <section class="flex min-w-0 flex-1 flex-col rounded-lg border border-border bg-surface">
    <header class="border-b border-border px-4 py-2 text-sm font-medium">{target}</header>

    <div bind:this={logEl} class="flex-1 overflow-y-auto px-4 py-3 font-mono text-sm leading-relaxed">
      {#each buffers[target] || [] as m}
        <div class="flex gap-2">
          <span class="text-muted shrink-0 text-xs tabular-nums opacity-60">{fmtTime(m.time)}</span>
          {#if m.kind === 'action'}
            <span class="text-accent italic">* {m.sender} {m.text}</span>
          {:else if m.kind === 'notice'}
            <span class="text-muted">-{m.sender}- {m.text}</span>
          {:else}
            <span><span class="text-accent">{m.sender}:</span> {m.text}</span>
          {/if}
        </div>
      {:else}
        <p class="text-muted text-xs">No messages yet.</p>
      {/each}
    </div>

    <div class="border-t border-border p-2">
      <input
        class="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent"
        placeholder={`Message ${target}`}
        bind:value={text}
        onkeydown={onKey}
      />
    </div>
  </section>
</div>
