<script>
  // Chat: a network's channels and private messages on the left, the
  // conversation in the middle (live over the stream WebSocket), members on
  // the right. ?net=ID&buf=NAME picks the buffer; ?mentions=1 opens the first
  // buffer that mentions you.
  import { onMount, onDestroy, tick } from 'svelte'
  import { page } from '$app/state'
  import { goto, replaceState } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import Select from '$lib/components/Select.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Msg from '$lib/components/Msg.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshNets } from '$lib/session.svelte.js'
  import { toast } from '$lib/ui.svelte.js'
  import { clock, hue } from '$lib/format.js'
  import { parse, describe, isChan, isEvent, COMMANDS, tableCells, isTableRule } from '$lib/chat.js'
  import { t, i18n } from '$lib/i18n.svelte.js'

  let nets = $state([])
  let netId = $state(null)
  let net = $derived(nets.find((n) => n.id === netId) || null)
  // mentions waiting on the networks you're not looking at, on the picker
  const otherMentions = $derived(nets.reduce((a, n) => a + (n.id === netId ? 0 : n.mentions || 0), 0))
  let bufs = $state([])            // [{ name, unread, mentions, readAt, joined, members, topic }]
  let buf = $state('')
  let logs = $state({})            // name -> message[]
  let readAt = $state({})          // name -> unix seconds the user had read up to when opening it
  let missed = $state({})          // name -> unread count when opened (for buffers never read before)
  let chan = $state(null)          // { topic, members }
  let text = $state('')
  let live = $state(false)
  let nick = $state('')
  let logEl = $state(), inputEl = $state()
  let showPane = $state(false)
  let stream = null
  let closed = false
  let poll

  const same = (a, b) => (a || '').toLowerCase() === (b || '').toLowerCase()
  const channels = $derived(bufs.filter((b) => isChan(b.name) && (b.joined || (net?.channels || []).some((c) => same(c, b.name)))))
  const queries = $derived(bufs.filter((b) => !isChan(b.name)))
  const ensure = (t) => { if (t && !logs[t]) logs[t] = [] }

  async function loadNets() {
    nets = (await api.networks()) || []
    session.nets = nets
  }
  async function loadBuffers() {
    if (!netId) return
    try { bufs = (await api.buffers(netId)) || [] } catch { /* keep */ }
  }

  async function scrollTo(mark) {
    await tick()
    if (!logEl) return
    const m = mark && logEl.querySelector('#backlog-mark')
    logEl.scrollTop = m ? m.offsetTop - 80 : logEl.scrollHeight
  }

  // Saved history is written asynchronously, so a fetch can miss lines that
  // already arrived live: merge rather than replace, dropping duplicates.
  const sig = (m) => `${m.time}|${m.kind}|${m.sender}|${m.text}|${m.extra || ''}`
  async function loadHistory(t) {
    ensure(t)
    let saved
    try { saved = (await api.messages(netId, t)) ?? [] } catch { return }
    const seen = new Set(saved.map(sig))
    const last = saved.at(-1)?.time ?? 0
    const keep = (logs[t] || []).filter((m) => m.kind === 'local' || (m.time >= last && !seen.has(sig(m))))
    logs[t] = [...saved, ...keep]
  }

  async function select(name, { pushUrl = true } = {}) {
    if (!same(name, buf)) typingDone()
    buf = name
    showPane = false
    const b = bufs.find((x) => same(x.name, name))
    readAt[name] = b && (b.unread || b.mentions) ? b.readAt : null
    missed[name] = b && !b.readAt ? b.unread || 0 : 0
    if (!logs[name]?.length) await loadHistory(name)
    scrollTo(!!readAt[name] || !!missed[name])
    if (pushUrl) {
      const u = new URL(location.href)
      u.searchParams.set('net', netId); u.searchParams.set('buf', name); u.searchParams.delete('mentions')
      replaceState(u, {})
    }
    loadChannel()
    if (b && (b.unread || b.mentions)) {
      api.markRead(netId, name).then(() => { loadBuffers(); refreshNets() }).catch(() => {})
    }
    inputEl?.focus()
  }

  async function loadChannel() {
    if (!buf || !isChan(buf)) { chan = null; return }
    try { chan = await api.channel(netId, buf) } catch { chan = null }
  }

  function local(t) {
    const target = buf || '*status'
    ensure(target)
    logs[target] = [...logs[target], { kind: 'local', text: t, time: Date.now() / 1000 }]
    scrollTo(false)
  }

  // Who's typing where: { target(lowercase): { nick: expiry ms } }. IRCv3
  // typing: active lasts 6 s unless renewed; done, or their message, ends it.
  let typers = $state({})
  let now = $state(Date.now())
  const TYPING_FOR = 6000
  function onTyping(f) {
    const k = f.target.toLowerCase()
    const cur = { ...(typers[k] || {}) }
    if (f.state === 'active') cur[f.nick] = Date.now() + TYPING_FOR
    else delete cur[f.nick] // done, or paused: not typing now
    typers = { ...typers, [k]: cur }
    now = Date.now()
  }
  function stopTyping(target, who) {
    const k = (target || '').toLowerCase()
    if (!typers[k]?.[who]) return
    const cur = { ...typers[k] }
    delete cur[who]
    typers = { ...typers, [k]: cur }
  }
  const typingHere = $derived(Object.entries(typers[(buf || '').toLowerCase()] || {}).filter(([, until]) => until > now).map(([n]) => n))
  const typingText = $derived(typingHere.length === 0 ? '' : typingHere.length === 1 ? t('chat.typingOne', { nick: typingHere[0] }) : typingHere.length === 2 ? t('chat.typingTwo', { a: typingHere[0], b: typingHere[1] }) : t('chat.typingMany'))
  // expire typers without a word from them
  $effect(() => {
    if (!typingHere.length) return
    const id = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(id)
  })

  // The user's own typing: active at most every 3 s while writing a message
  // (not a /command), done when the box is cleared or they leave the buffer.
  let typedTo = '' // the buffer we last said "active" to
  let typedAt = 0
  function typed() {
    const msg = text.trim() !== '' && !text.startsWith('/') && buf && buf !== '*status'
    if (msg) {
      if (typedTo !== buf || Date.now() - typedAt > 3000) { stream?.typing(buf, 'active'); typedTo = buf; typedAt = Date.now() }
    } else if (typedTo) typingDone()
  }
  function typingDone() {
    if (typedTo) stream?.typing(typedTo, 'done')
    typedTo = ''; typedAt = 0
  }

  function onMsg(m) {
    const me = same(m.sender, nick)
    if (m.sender) stopTyping(m.target, m.sender)
    if (m.kind === 'nick' && me) nick = m.extra
    if (['join', 'part', 'kick'].includes(m.kind)) { loadBuffers(); if (same(m.target, buf)) loadChannel() }
    if (m.kind === 'part' && me) {
      if (same(buf, m.target)) { const next = channels.find((c) => !same(c.name, m.target)); if (next) select(next.name) }
      return
    }
    ensure(m.target)
    logs[m.target] = [...logs[m.target], m]
    if (same(m.target, buf)) {
      scrollTo(false)
      if (!me && !isEvent(m.kind)) api.markRead(netId, buf).catch(() => {})
    } else if (!isEvent(m.kind)) {
      // bump the badge locally until the next refresh
      const b = bufs.find((x) => same(x.name, m.target))
      if (b) b.unread++
      else bufs = [...bufs, { name: m.target, unread: 1, mentions: isChan(m.target) ? 0 : 1 }]
    }
  }

  function openStream() {
    stream?.close()
    const id = netId
    live = false
    typers = {}
    stream = api.openStream(id, onMsg, () => {
      live = false
      if (!closed && id === netId) setTimeout(() => !closed && id === netId && openStream(), 3000)
    }, () => { if (id === netId) live = true }, onTyping)
  }

  // want: a buffer to open instead of the first unread one (opening a buffer
  // marks it read, so don't touch others on the way)
  async function switchNet(id, want = '') {
    netId = id
    logs = {}; readAt = {}; missed = {}; buf = ''; chan = null
    nick = net?.currentNick || net?.nick || ''
    if (net && net.status === 'disconnected') {
      try { await api.connect(id); toast(t('chat.connecting', { network: net.name })) } catch (e) { toast(e.message, 'bad') }
    }
    await loadBuffers()
    if (want) {
      ensure(want)
      if (!bufs.some((b) => same(b.name, want))) bufs = [...bufs, { name: want, unread: 0, mentions: 0 }]
      await select(want)
    } else {
      const first = (page.url.searchParams.get('mentions') && bufs.find((b) => b.mentions)) || channels.find((b) => b.unread || b.mentions) || channels[0] || queries[0]
      if (first) await select(first.name)
    }
    setTimeout(openStream, net?.status === 'connected' ? 0 : 1500)
  }

  async function send(e) {
    e.preventDefault()
    const cmd = parse(text, buf)
    if (!cmd) return
    const fail = () => { toast(t('chat.notConnected'), 'bad'); return false }
    if (cmd.error) { local(cmd.error); return }
    if (cmd.help) { text = ''; for (const [c, d] of COMMANDS) local(`${c} — ${t(d)}`); return }
    if (cmd.close) {
      text = ''
      if (isChan(buf)) { local(t('chat.closeChannel')); return }
      bufs = bufs.filter((b) => !same(b.name, buf))
      const next = channels[0] || queries[0]
      if (next) select(next.name); else buf = ''
      return
    }
    if (cmd.say !== undefined) {
      if (!buf) { toast(t('chat.pickChannelFirst'), 'bad'); return }
      if (!stream?.send(buf, cmd.say)) return fail()
      typedTo = ''; typedAt = 0 // the message ends the typing
    }
    if (cmd.raw && !stream?.raw(cmd.raw)) return fail()
    if (cmd.status !== undefined) {
      if (!stream?.status(cmd.status)) return fail()
      ensure('*status')
      logs['*status'] = [...logs['*status'], { kind: 'message', sender: nick, text: cmd.status, time: Date.now() / 1000 }]
      if (!bufs.some((b) => b.name === '*status')) bufs = [...bufs, { name: '*status', unread: 0, mentions: 0 }]
    }
    if (cmd.send && !stream?.send(cmd.send.target, cmd.send.text, cmd.send.kind)) return fail()
    text = ''
    if (cmd.open) {
      ensure(cmd.open)
      if (!bufs.some((b) => same(b.name, cmd.open))) bufs = [...bufs, { name: cmd.open, unread: 0, mentions: 0 }]
      await select(cmd.open)
    } else if (cmd.raw?.startsWith('JOIN ')) {
      const ch = cmd.raw.split(' ')[1]
      ensure(ch)
      if (!bufs.some((b) => same(b.name, ch))) bufs = [...bufs, { name: ch, unread: 0, mentions: 0, joined: true }]
      await select(ch)
    }
  }

  async function markAllRead() {
    try { await api.markRead(netId); await loadBuffers(); refreshNets(); toast(t('chat.markedRead', { network: net.name })) } catch (e) { toast(e.message, 'bad') }
  }

  onMount(async () => {
    try { await loadNets() } catch (e) { toast(e.message, 'bad'); return }
    const q = page.url.searchParams
    const want = +q.get('net') || (q.get('mentions') && nets.find((n) => n.mentions)?.id) || (nets.find((n) => n.status === 'connected') || nets[0])?.id
    if (!want) return
    await switchNet(want, q.get('buf') || '')
    poll = setInterval(() => { loadBuffers(); loadNets().catch(() => {}); if (isChan(buf)) loadChannel() }, 10000)
  })
  onDestroy(() => { closed = true; stream?.close(); clearInterval(poll) })

  // Day separators and the "missed while away" mark.
  const lines = $derived.by(() => {
    const out = []
    let day = ''
    let marked = false
    const mark = readAt[buf]
    const list = logs[buf] || []
    const chat = (m) => !isEvent(m.kind) && m.kind !== 'local' && m.kind !== 'system'
    // where the unread lines start: after the read marker, or (never read)
    // the last `missed` chat lines
    let start = -1
    if (mark) start = list.findIndex((m) => m.time > mark && chat(m))
    else if (missed[buf]) {
      for (let i = list.length - 1, n = 0; i >= 0; i--) if (chat(list[i]) && ++n === missed[buf]) { start = i; break }
    }
    const after = start < 0 ? 0 : list.slice(start).filter(chat).length
    for (const [i, m] of list.entries()) {
      const d = new Date(m.time * 1000)
      const key = d.toDateString()
      if (key !== day) {
        day = key
        const today = new Date().toDateString(), yest = new Date(Date.now() - 86400000).toDateString()
        out.push({ sep: key === today ? t('chat.today') : key === yest ? t('chat.yesterday') : d.toLocaleDateString(i18n.intl, { weekday: 'long', day: 'numeric', month: 'short' }) })
      }
      if (!marked && i === start && after) {
        marked = true
        out.push({ backlog: t('chat.missed', { count: after }) })
      }
      // *status's Markdown tables (help…): consecutive rows make one table,
      // the first its header
      const cells = m.sender === '*status' && m.kind === 'message' ? tableCells(m.text) : null
      if (cells) {
        const last = out.at(-1)
        if (last?.table) { if (!isTableRule(cells)) last.table.push(cells) } else out.push({ table: [cells], time: m.time, sender: m.sender })
        continue
      }
      out.push(m)
    }
    return out
  })
  const isHighlight = (m) => !same(m.sender, nick) && nick && new RegExp(`(^|[^\\w\\[\\]\\\\\`^{|}-])${nick.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}($|[^\\w\\[\\]\\\\\`^{|}-])`, 'i').test(m.text || '')
  const groups = $derived.by(() => {
    if (!chan || !isChan(buf)) return [[t('chat.inThisChat'), '', [nick, buf].filter(Boolean)]]
    const g = [[t('chat.operators'), '@', []], [t('chat.halfOps'), '%', []], [t('chat.voiced'), '+', []], [t('chat.everyoneElse'), '', []]]
    for (const m of chan.members || []) {
      const p = m.prefix
      const i = p === '~' || p === '&' || p === '@' ? 0 : p === '%' ? 1 : p === '+' ? 2 : 3
      g[i][2].push(m)
    }
    return g.filter(([, , list]) => list.length)
  })
</script>

<svelte:head><title>{buf ? `${buf} · ${net?.name}` : t('chat.title')} · evrd</title></svelte:head>

{#if !nets.length}
  <div style="padding:var(--s7)"><div class="empty card"><span class="ic"><Icon name="globe" /></span><h3>{t('chat.noNetworks')}</h3><p>{t('chat.noNetworksHelp')}</p><a class="btn btn-primary btn-sm" href="/networks/new"><Icon name="plus" />{t('chat.addNetwork')}</a></div></div>
{:else}
  <div class="chat">
    <nav class="buffers" class:open={showPane} id="buffers" aria-label={t('chat.buffers')}>
      <div class="net-switch">
        <div class="od-row"><span class="caps">{t('chat.network')}</span>{#if net}<StatusChip status={net.status} />{/if}</div>
        <div class="net-select">
          <label class="sr-only" for="net-pick">{t('chat.network')}</label>
          <Select id="net-pick" strong value={netId} onchange={(v) => switchNet(v)} testid="net-pick"
            options={nets.map((n) => ({ ...n, value: n.id, label: n.name }))}>
            {#snippet option(n, closed)}
              <span class="net-dot {n.status}" aria-hidden="true"></span>
              <span class="od-truncate net-opt-name">{n.name}</span>
              {#if closed}
                {#if otherMentions}<span class="count mention" title={t('chat.otherMentionsTitle')} aria-label={t('chat.otherMentions', { count: otherMentions })}>{otherMentions}</span>{/if}
              {:else if n.mentions}<span class="count mention" aria-label={t('chat.mentions', { count: n.mentions })}>{n.mentions}</span>
              {:else if n.unread}<span class="count" aria-label={t('chat.unread', { count: n.unread })}>{n.unread}</span>{/if}
            {/snippet}
          </Select>
        </div>
        {#if net}
          <div class="od-row" style="justify-content:space-between;font-size:var(--fs-xs)">
            <span class="muted mono od-truncate">{nick} · {net.host}</span>
            <span class="od-nowrap muted">{net.lag != null ? t('chat.lag', { ms: net.lag }) : net.status === 'connected' ? t('chat.measuringLag') : t('chat.noLink')}</span>
          </div>
        {/if}
      </div>
      <div class="od-stack" style="--od-gap:4px">
        <span class="caps" style="padding:0 12px">{t('chat.channels')}</span>
        <div class="buf-list">
          {#each channels as b (b.name)}
            <button class="buf" class:unread={b.unread || b.mentions} type="button" aria-current={same(b.name, buf)} onclick={() => select(b.name)}>
              <Icon name="hash" /><span class="od-truncate">{b.name.replace(/^#/, '')}</span>
              {#if b.mentions}<span class="count mention" aria-label={t('chat.mentions', { count: b.mentions })}>{b.mentions}</span>{:else if b.unread}<span class="count" class:hot={b.unread > 30} aria-label={t('chat.unread', { count: b.unread })}>{b.unread}</span>{/if}
            </button>
          {:else}
            <p class="desc" style="padding:0 12px;font-size:var(--fs-xs)"><Msg key="chat.noChannels">{#snippet join()}<span class="code">/join #channel</span>{/snippet}</Msg></p>
          {/each}
        </div>
      </div>
      <div class="od-stack" style="--od-gap:4px">
        <span class="caps" style="padding:0 12px">{t('chat.messages')}</span>
        <div class="buf-list">
          {#each queries as b (b.name)}
            <button class="buf" class:unread={b.unread || b.mentions} type="button" aria-current={same(b.name, buf)} onclick={() => select(b.name)}>
              <Icon name="user" /><span class="od-truncate">{b.name}</span>
              {#if b.mentions || b.unread}<span class="count mention">{b.mentions || b.unread}</span>{/if}
            </button>
          {:else}
            <p class="desc" style="padding:0 12px;font-size:var(--fs-xs)">{t('chat.noQueries')}</p>
          {/each}
        </div>
      </div>
    </nav>

    <section class="log-pane" aria-labelledby="buf-title">
      <header class="log-head">
        <button class="icon-btn pane-toggle" type="button" aria-controls="buffers" aria-expanded={showPane} aria-label={t('chat.showBuffers')} onclick={() => (showPane = !showPane)}><Icon name="panel" /></button>
        <div class="od-field od-fill">
          <span id="buf-title" class="od-truncate">{buf || net?.name}</span>
          <span class="topic od-truncate">{isChan(buf) ? chan?.topic || t('chat.noTopic') : buf ? t('chat.privateOn', { network: net?.name }) : ''}</span>
        </div>
        <span class="live" class:on={live} title={live ? t('chat.live') : t('chat.reconnecting')}><span class="dot" aria-hidden="true"></span>{live ? t('chat.live') : t('chat.offline')}</span>
        <div class="actions">
          <button class="icon-btn hide-sm" type="button" aria-label={t('chat.markAllRead')} onclick={markAllRead}><Icon name="check" /></button>
          <a class="icon-btn" href="/networks/{netId}" aria-label={t('chat.networkSettings')}><Icon name="sliders" /></a>
        </div>
      </header>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div class="log" bind:this={logEl} role="log" aria-live="polite" aria-labelledby="buf-title" tabindex="0">
        {#each lines as m}
          {#if m.sep}<div class="day-sep">{m.sep}</div>
          {:else if m.backlog}<div class="day-sep backlog" id="backlog-mark">{m.backlog}</div>
          {:else if m.kind === 'local' || m.kind === 'system'}<div class="msg event"><time></time><span class="nick">·</span><span class="text">{m.text}</span></div>
          {:else if isEvent(m.kind)}<div class="msg event ev-{m.kind}"><time>{clock(m.time)}</time><span class="nick">{m.kind === 'join' ? '→' : ['part', 'quit', 'kick'].includes(m.kind) ? '←' : '•'}</span><span class="text">{describe(m)}</span></div>
          {:else if m.kind === 'action'}<div class="msg" class:self={same(m.sender, nick)}><time>{clock(m.time)}</time><span class="nick" style="--c:{hue(m.sender)}">*</span><span class="text"><span style="color:var(--c, {hue(m.sender)})">{m.sender}</span> {m.text}</span></div>
          {:else if m.table}<div class="msg" data-testid="status-table"><time>{clock(m.time)}</time><span class="nick" style="--c:{hue(m.sender)}">{m.sender}</span><div class="text md-wrap"><table class="md-table"><thead><tr>{#each m.table[0] as c}<th scope="col">{c}</th>{/each}</tr></thead><tbody>{#each m.table.slice(1) as r}<tr>{#each r as c, i}<td class:cmd={i < 2}>{c}</td>{/each}</tr>{/each}</tbody></table></div></div>
          {:else}<div class="msg" class:self={same(m.sender, nick)} class:highlight={isHighlight(m)}><time>{clock(m.time)}</time><span class="nick" style="--c:{hue(m.sender)}" title={m.sender}>{m.kind === 'notice' ? `-${m.sender}-` : m.sender}</span><span class="text">{m.text}</span></div>
          {/if}
        {:else}
          <p class="desc">{buf ? t('chat.noMessages') : t('chat.pickChannel')}</p>
        {/each}
      </div>
      <p class="typing" aria-live="polite" data-testid="typing">{typingText}</p>
      <form class="composer" onsubmit={send}>
        <label class="sr-only" for="say">{t('chat.message')}</label>
        <input class="input od-fill" id="say" autocomplete="off" placeholder={buf ? t('chat.messagePlaceholder', { buffer: buf }) : '/join #channel'} bind:value={text} bind:this={inputEl} aria-label={t('chat.message')} oninput={typed} />
        <button class="btn btn-primary" type="submit" aria-label={t('chat.send')} style="min-height:48px;min-width:48px;padding:0 14px" disabled={!text.trim()}><Icon name="send" /><span class="hide-sm">{t('chat.send')}</span></button>
      </form>
    </section>

    <aside class="members" aria-labelledby="mem-h">
      <div class="member-head"><h2 id="mem-h" class="caps">{t('chat.members')}</h2><span class="count calm">{isChan(buf) ? (chan?.members?.length ?? 0) : 2}</span></div>
      {#each groups as [g, mode, list]}
        <div class="member-group">
          <span class="caps" style="padding:12px 8px 4px">{g}</span>
          {#each list as mbr}
            {@const name = typeof mbr === 'string' ? mbr : mbr.nick}
            <span class="member"><span class="mode" aria-label={mode === '@' ? t('chat.modeOp') : mode === '+' ? t('chat.modeVoice') : mode === '%' ? t('chat.modeHalfOp') : ''}>{typeof mbr === 'string' ? '' : mbr.prefix}</span><span class="od-truncate" style="color:{same(name, nick) ? 'var(--primary)' : 'inherit'}">{name}</span></span>
          {/each}
        </div>
      {/each}
    </aside>
  </div>
{/if}

<style>
  /* *status's tables (help…). The chat's text column is narrow, so each row
     stacks: its first cells on one line (command, arguments), the last below. */
  .md-table, .md-table tbody { display: block; width: auto; border-spacing: 0; font-size: var(--fs-sm); }
  .md-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .md-table tr { display: flex; flex-wrap: wrap; column-gap: 10px; padding: 6px 0; border-bottom: 1px solid var(--line-soft); cursor: auto; }
  .md-table tr:last-child { border-bottom: 0; }
  .md-table td { display: block; padding: 0; background: none; border-radius: 0; }
  .md-table tbody tr:hover td { background: none; }
  .md-table td.cmd { font-family: var(--font-mono); color: var(--muted); }
  .md-table td.cmd:first-child { color: var(--primary); }
  .md-table td:last-child { flex-basis: 100%; }
  .net-select { position: relative; }
  .net-dot { flex: none; width: 8px; height: 8px; border-radius: 50%; background: var(--faint); }
  .net-dot.connected { background: var(--ok, var(--primary)); }
  .net-dot.reconnecting, .net-dot.connecting { background: var(--warn); }
  .net-opt-name { flex: 1; min-width: 0; }
  .member-head { display: flex; justify-content: space-between; align-items: center; }
  .log-head .actions { margin-left: auto; display: flex; gap: var(--s2); }
  .topic { max-width: 64ch; }
  .live { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-xs); color: var(--faint); }
  .live .dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
  .live.on { color: var(--ok); }
  .live.on .dot { animation: pulse 2s var(--ease-out) infinite; }
  :global(.msg.ev-join .nick) { color: var(--ok); }
  :global(.msg.ev-part .nick), :global(.msg.ev-quit .nick), :global(.msg.ev-kick .nick) { color: var(--bad); }
</style>
