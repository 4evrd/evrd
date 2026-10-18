<script>
  // A network's buffers in the browser: channel/PM list on the left, the
  // conversation on the right, live over the stream WebSocket.
  import { onMount, onDestroy, tick } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import { api } from '$lib/api.js'
  import { toast } from '$lib/ui.svelte.js'
  import { clock } from '$lib/format.js'
  import { parse, describe, isChan, isEvent, COMMANDS } from '$lib/chat.js'

  const id = Number(page.params.id)
  let net = $state(null)
  let missing = $state(false)
  let buffers = $state({}) // target -> message[]
  let joined = $state([])   // channels we're in
  let nick = $state('')     // our live nick
  let target = $state('')
  let text = $state('')
  let live = $state(false)
  let logEl = $state()
  let inputEl = $state()
  let showList = $state(false) // mobile: buffer list drawer
  let stream = null
  let closed = false

  const ensure = (t) => { if (t && !buffers[t]) buffers[t] = [] }
  const same = (a, b) => (a || '').toLowerCase() === (b || '').toLowerCase()
  // Joined channels, then private buffers.
  const targets = $derived([
    ...[...joined].sort((a, b) => a.localeCompare(b)),
    ...Object.keys(buffers).filter((t) => !isChan(t)).sort((a, b) => a.localeCompare(b)),
  ])
  // A line only this browser sees: command errors, /help.
  function local(text, t = target) {
    if (!t) { toast(text, 'err'); return }
    ensure(t)
    buffers[t] = [...buffers[t], { kind: 'local', text, time: Date.now() / 1000 }]
    scrollDown()
  }

  async function scrollDown() {
    await tick()
    if (logEl) logEl.scrollTop = logEl.scrollHeight
  }

  // Saved history is written asynchronously, so a fetch can miss lines that
  // already arrived live: merge rather than replace, dropping duplicates.
  const sig = (m) => `${m.time}|${m.kind}|${m.sender}|${m.text}|${m.extra || ''}`
  async function loadHistory(t) {
    ensure(t)
    let saved
    try { saved = (await api.messages(id, t)) ?? [] } catch { return /* keep what we have */ }
    const seen = new Set(saved.map(sig))
    const last = saved.at(-1)?.time ?? 0
    const live = (buffers[t] || []).filter((m) => m.kind === 'local' || (m.time >= last && !seen.has(sig(m))))
    buffers[t] = [...saved, ...live]
    await scrollDown()
  }

  async function select(t) {
    target = t
    showList = false
    if (!buffers[t]?.length) await loadHistory(t)
    else await scrollDown()
    inputEl?.focus()
  }

  function onMsg(m) {
    // Our own joins, parts, kicks and nick changes reshape the buffer list.
    const me = same(m.sender, nick)
    if (m.kind === 'join' && me && !joined.some((c) => same(c, m.target))) joined = [...joined, m.target]
    if (m.kind === 'part' && me) {
      joined = joined.filter((c) => !same(c, m.target))
      if (same(target, m.target)) target = joined[0] || targets[0] || ''
    }
    if (m.kind === 'kick' && same(m.extra, nick)) joined = joined.filter((c) => !same(c, m.target))
    if (m.kind === 'nick' && me) nick = m.extra
    if (m.kind === 'part' && me) return
    ensure(m.target)
    buffers[m.target] = [...buffers[m.target], m]
    if (m.target === target) scrollDown()
  }

  function openStream() {
    stream = api.openStream(id, onMsg, () => {
      live = false
      if (!closed) setTimeout(() => !closed && openStream(), 3000) // reconnect
    })
    live = true
  }

  async function send(e) {
    e.preventDefault()
    const cmd = parse(text, target)
    if (!cmd) return
    const fail = () => { toast('Not connected — nothing was sent', 'err'); return false }
    if (cmd.error) { local(cmd.error); return }
    if (cmd.help) { text = ''; for (const [c, d] of COMMANDS) local(`${c} — ${d}`); return }
    if (cmd.close) {
      text = ''
      if (isChan(target)) { local('That’s a channel — /part to leave it.'); return }
      delete buffers[target]
      target = joined[0] || targets[0] || ''
      return
    }
    if (cmd.say !== undefined) {
      if (!target) { toast('Pick a channel first', 'err'); return }
      if (!stream?.send(target, cmd.say)) return fail()
    }
    if (cmd.raw && !stream?.raw(cmd.raw)) return fail()
    if (cmd.status !== undefined) {
      if (!stream?.status(cmd.status)) return fail()
      // echo the command into the *status buffer, like a sent message
      ensure('*status')
      buffers['*status'] = [...buffers['*status'], { kind: 'message', sender: nick, text: cmd.status, time: Date.now() / 1000 }]
    }
    if (cmd.send && !stream?.send(cmd.send.target, cmd.send.text, cmd.send.kind)) return fail()
    text = ''
    if (cmd.open) { ensure(cmd.open); await select(cmd.open) }
    else if (cmd.raw?.startsWith('JOIN ')) {
      const ch = cmd.raw.split(' ')[1]
      ensure(ch)
      await select(ch)
    }
  }

  onMount(async () => {
    try {
      net = (await api.networks()).find((n) => n.id === id) || null
    } catch { /* handled below */ }
    if (!net) { missing = true; return }
    nick = net.currentNick || net.nick
    joined = [...net.channels]
    if (net.status === 'disconnected') {
      try { await api.connect(id); net.status = 'connecting' } catch (e) { toast(e.message, 'err') }
    }
    for (const ch of net.channels) ensure(ch)
    // private buffers with history
    try { for (const b of (await api.buffers(id)) || []) if (!isChan(b)) ensure(b) } catch { /* channels still show */ }
    target = net.channels[0] || targets[0] || ''
    if (target) await loadHistory(target)
    // The stream needs a live upstream; give a fresh connect a moment.
    setTimeout(openStream, net.status === 'connected' ? 0 : 1500)
  })
  onDestroy(() => { closed = true; stream?.close() })
</script>

<svelte:head><title>{net ? `${target || net.name} · ${net.name}` : 'Network'} · evrd</title></svelte:head>

{#if missing}
  <div class="pad">
    <div class="empty"><span class="ic"><Icon name="globe" /></span><h4>Network not found</h4><p>It may have been removed.</p><a class="btn btn-sm" href="/networks">Back to networks</a></div>
  </div>
{:else if !net}
  <div class="pad"><div class="skel"></div><div class="skel"></div></div>
{:else}
  <div class="chat">
    <aside class="bufs" class:open={showList} aria-label="Channels and messages">
      <div class="bufs-head">
        <a class="link-btn" href="/networks"><Icon name="chevron" class="back" />Networks</a>
        <StatusChip status={net.status} />
      </div>
      <div class="net-name">{net.name}</div>
      <nav class="buf-list">
        {#each targets as t (t)}
          <button type="button" class="buf" aria-current={t === target ? 'true' : undefined} onclick={() => select(t)}>
            <Icon name={isChan(t) ? 'hash' : 'user'} /><span>{isChan(t) ? t.slice(1) : t}</span>
          </button>
        {:else}
          <p class="hint">No channels yet. <span class="code">/join #channel</span> to join one.</p>
        {/each}
      </nav>
    </aside>

    <section class="conv" aria-label={target || net.name}>
      <header class="conv-head">
        <button class="icon-btn list-btn" type="button" aria-label="Channels" aria-expanded={showList} onclick={() => (showList = !showList)}><Icon name="hash" /></button>
        <h1>{target || net.name}</h1>
        <span class="who">as <b>{nick}</b></span>
        <a class="icon-btn settings" href="/networks#edit-{net.id}" aria-label="Edit {net.name}" title="Network settings"><Icon name="settings" /></a>
        <span class="live" class:on={live} title={live ? 'Live' : 'Reconnecting…'}><span class="dot" aria-hidden="true"></span>{live ? 'Live' : 'Offline'}</span>
      </header>

      <div class="log" bind:this={logEl} role="log" aria-live="polite">
        {#each buffers[target] || [] as m}
          <div class="msg {m.kind}">
            <time>{clock(m.time)}</time>
            {#if m.kind === 'action'}
              <span class="body"><span class="act">* {m.sender} {m.text}</span></span>
            {:else if m.kind === 'notice'}
              <span class="body"><span class="from">-{m.sender}-</span> {m.text}</span>
            {:else if m.kind === 'system' || m.kind === 'local'}
              <span class="body sys">{m.text}</span>
            {:else if isEvent(m.kind)}
              <span class="body ev ev-{m.kind}">{describe(m)}</span>
            {:else}
              <span class="body"><span class="from">{m.sender}</span> {m.text}</span>
            {/if}
          </div>
        {:else}
          <p class="hint">{target ? 'No messages yet.' : 'Pick a channel.'}</p>
        {/each}
      </div>

      <form class="compose" onsubmit={send}>
        <input class="input" placeholder={target ? `Message ${target} — /help for commands` : '/join #channel'} aria-label="Message" bind:value={text} bind:this={inputEl} autocomplete="off" />
        <button class="btn btn-primary" type="submit" disabled={!text.trim()}>Send</button>
      </form>
    </section>
  </div>
{/if}

<style>
  .pad { padding: clamp(20px, 3vw, 36px) clamp(16px, 3vw, 32px); display: grid; gap: var(--s4); }
  .chat { height: 100%; display: grid; grid-template-columns: 240px minmax(0, 1fr); min-height: 0; }
  .bufs { border-right: 1px solid var(--line); padding: var(--s4) var(--s3); display: grid; grid-template-rows: auto auto minmax(0, 1fr); gap: var(--s3); min-height: 0; background: oklch(17% 0.015 252 / 0.6); }
  .bufs-head { display: flex; align-items: center; justify-content: space-between; gap: var(--s2); }
  .bufs-head :global(.back) { transform: rotate(180deg); width: 14px; height: 14px; margin-right: 2px; }
  .net-name { font-size: var(--fs-xs); font-weight: 500; letter-spacing: 0.06em; text-transform: uppercase; color: var(--faint); padding: 0 var(--s3); }
  .buf-list { display: grid; gap: 2px; align-content: start; overflow-y: auto; }
  .buf { display: flex; align-items: center; gap: var(--s2); min-height: 40px; padding: 0 var(--s3); border-radius: var(--r-row); border: 1px solid transparent; background: none; color: var(--muted); font-family: var(--font-mono); font-size: 14px; text-align: left; cursor: pointer; }
  .buf span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .buf :global(svg.i) { width: 15px; height: 15px; }
  .buf:hover { background: var(--row); color: var(--fg); }
  .buf[aria-current] { color: var(--fg); background: oklch(83% 0.16 162 / 0.10); border-color: oklch(83% 0.16 162 / 0.3); }
  .buf[aria-current] :global(svg.i) { color: var(--link); }
  .hint { color: var(--faint); font-size: var(--fs-sm); padding: var(--s2) var(--s3); }

  .conv { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-height: 0; min-width: 0; }
  .conv-head { display: flex; align-items: center; gap: var(--s3); padding: var(--s3) var(--s5); border-bottom: 1px solid var(--line); min-height: 60px; }
  .conv-head h1 { font-size: var(--fs-lg); font-family: var(--font-mono); font-weight: 500; letter-spacing: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .conv-head .who { color: var(--faint); font-size: var(--fs-sm); margin-right: auto; white-space: nowrap; }
  .conv-head .who b { color: var(--muted); font-weight: 500; font-family: var(--font-mono); }
  .live { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; color: var(--faint); }
  .live .dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
  .live.on { color: var(--ok); }
  .live.on .dot { animation: pulse 2s ease-out infinite; }
  .list-btn { display: none; }

  .log { overflow-y: auto; overscroll-behavior: contain; padding: var(--s4) var(--s5); font-family: var(--font-mono); font-size: 14px; line-height: 1.7; background: oklch(13% 0.013 252 / 0.5); }
  .msg { display: flex; gap: var(--s3); }
  .msg time { color: var(--faint); opacity: .7; font-size: 12px; flex: none; padding-top: 2px; font-variant-numeric: tabular-nums; }
  .msg .body { min-width: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
  .msg .from { color: var(--nick); }
  .msg .act { color: var(--link); font-style: italic; }
  .msg.notice .body { color: var(--muted); }
  .msg .sys { color: var(--faint); }
  .msg .ev { color: var(--faint); font-size: 13px; }
  .msg .ev-join::before { content: '→ '; color: var(--ok); }
  .msg .ev-part::before, .msg .ev-quit::before, .msg .ev-kick::before { content: '← '; color: var(--bad); }
  .msg .ev-nick::before, .msg .ev-mode::before { content: '• '; }
  .msg .ev-topic { color: var(--muted); }
  .conv-head .settings { width: 36px; height: 36px; }

  .compose { display: flex; gap: var(--s2); padding: var(--s3) var(--s4); border-top: 1px solid var(--line); }
  .compose .input { flex: 1; min-width: 0; }

  @media (max-width: 759px) {
    .chat { grid-template-columns: minmax(0, 1fr); position: relative; }
    .bufs { position: absolute; z-index: 20; inset: 0 auto 0 0; width: min(280px, 85%); transform: translateX(-102%); transition: transform var(--t-med) var(--ease-out); background: oklch(19% 0.016 252 / 0.97); }
    .bufs.open { transform: none; box-shadow: var(--shadow); }
    .list-btn { display: inline-grid; }
    .conv-head { padding-inline: var(--s3); }
    .log { padding-inline: var(--s3); }
    .msg { gap: var(--s2); }
  }
</style>
