<script>
  // The full network settings page, ZNC-style: servers (tried in turn),
  // identity, SASL, certificate trust, channels and the advanced knobs.
  // id = null adds a network.
  import { onMount, onDestroy, tick } from 'svelte'
  import { goto } from '$app/navigation'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Select from './Select.svelte'
  import Switch from './Switch.svelte'
  import StatusChip from './StatusChip.svelte'
  import ConnLog from './ConnLog.svelte'
  import DirectoryPicker from './DirectoryPicker.svelte'
  import { api } from '$lib/api.js'
  import { fromDirectory, loginHint } from '$lib/directory.js'
  import { session, refreshNets, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { check, splitChannels } from '$lib/validate.js'

  let { id = null } = $props()
  const me = $derived(session.me)
  const settings = $derived(me.settings || {})

  const ENCODINGS = ['ISO-8859-1', 'ISO-8859-15', 'windows-1252', 'ISO-8859-2', 'windows-1250', 'windows-1251', 'KOI8-R', 'ISO-8859-7', 'ISO-8859-9', 'Shift_JIS', 'EUC-JP', 'ISO-2022-JP', 'GBK', 'Big5', 'EUC-KR']
  const SECTIONS = [['servers', 'Servers'], ['identity', 'Identity'], ['sasl', 'Authentication'], ['tls', 'Certificates'], ['channels', 'Channels'], ['modules', 'Modules'], ['advanced', 'Advanced']]

  // Modules: what evrd offers (and an admin hasn't switched off server-wide)
  let available = $state([])
  api.modules().then((m) => (available = m)).catch(() => {})
  const mod = (name) => (f.modules[name] ||= { enabled: false, settings: {} })
  function setMod(name, on) { mod(name).enabled = on }
  const val = (m, s) => f.modules[m.name]?.settings?.[s.key] ?? s.default ?? ''
  function setVal(m, s, v) { mod(m.name).settings[s.key] = v }

  // The live state and the connection log, refreshed while the page is open
  // (the form keeps what's being edited).
  let live = $state(null)
  let log = $state(null)
  let logTimer
  async function poll() {
    if (id == null) return
    try { const [n, l] = await Promise.all([api.network(id), api.networkLog(id)]); live = n; log = l } catch { /* keep the last */ }
  }
  onDestroy(() => clearInterval(logTimer))
  const state = $derived(live || net)
  async function copyLog() {
    const text = (log || []).map((e) => `${new Date(e.at).toISOString()} ${e.from}${e.bad ? ' !' : ''} ${e.text}`).join('\n')
    try { await navigator.clipboard.writeText(text); toast('Copied the connection log') } catch { toast('Couldn’t copy', 'bad') }
  }

  let net = $state(null)          // as loaded (null while adding)
  let f = $state(null)            // the form
  let binds = $state({ hosts: [], locked: false, any: false })
  let errs = $state({})
  let formErr = $state('')
  let saving = $state(false)
  let loadErr = $state('')
  let newChan = $state('')
  let newFp = $state('')

  function fromNet(n) {
    const cs = n.channelSettings || {}
    return {
      modules: Object.fromEntries(Object.entries(n.modules || {}).map(([k, v]) => [k, { enabled: !!v.enabled, settings: { ...(v.settings || {}) } }])),
      name: n.name, enabled: n.enabled,
      servers: (n.servers?.length ? n.servers : [{ host: n.host, port: n.port, tls: n.tls }]).map((s) => ({ host: s.host, port: String(s.port), tls: s.tls, hasPassword: !!s.hasPassword, password: undefined })),
      nick: n.nick, altNick: n.altNick || '', username: n.username || '', realname: n.realname || '', quitMsg: n.quitMsg || '', bindHost: n.bindHost || '',
      saslUser: n.saslUser || '', password: undefined, hasPassword: n.hasPassword,
      trustPKI: n.trustPKI, trustAllCerts: n.trustAllCerts, fingerprints: [...(n.fingerprints || [])],
      encodingName: (n.encoding || '').replace(/^\^/, ''), encodingAlways: !!n.encoding && !n.encoding.startsWith('^'),
      floodOn: n.floodRate > 0, floodRate: String(n.floodRate > 0 ? n.floodRate : 2), floodBurst: String(n.floodBurst || 9), joinDelay: String(n.joinDelay || 0),
      channels: (n.channels || []).map((c) => ({ name: c, key: cs[c.toLowerCase()]?.key || '', detached: !!cs[c.toLowerCase()]?.detached, disabled: !!cs[c.toLowerCase()]?.disabled })),
    }
  }
  function blank() {
    return {
      modules: {},
      name: '', enabled: true,
      servers: [{ host: '', port: '6697', tls: true, hasPassword: false, password: undefined }],
      nick: me.defaultNick || me.username, altNick: '', username: '', realname: '', quitMsg: '', bindHost: '',
      saslUser: '', password: undefined, hasPassword: false,
      trustPKI: true, trustAllCerts: false, fingerprints: [],
      encodingName: '', encodingAlways: false,
      floodOn: true, floodRate: '2', floodBurst: '9', joinDelay: '0', channels: [],
    }
  }

  onMount(async () => {
    poll()
    logTimer = setInterval(poll, 5000)
    try { const b = await api.bindHosts(); binds = { ...b, hosts: b.hosts || [] } } catch { /* no bind hosts */ }
    if (id == null) { f = blank(); return }
    try { net = await api.network(id); f = fromNet(net) } catch (e) { loadErr = e.status === 404 ? 'That network doesn’t exist (or isn’t yours).' : e.message }
  })

  // ---- the network directory (adding only): picking one fills in its
  // name, servers and suggested channels; identity stays the user's.
  let manual = $state(false)
  let picked = $state(null)
  function pick(n) {
    const p = fromDirectory(n)
    f.name = p.name
    f.servers = p.servers
    f.channels = p.channels
    picked = n
    errs = {}
  }
  function enterManually() {
    manual = true
    tick().then(() => document.getElementById('name')?.focus())
  }

  // ---- servers
  function addServer() { f.servers = [...f.servers, { host: '', port: f.servers[0]?.tls === false ? '6667' : '6697', tls: f.servers[0]?.tls ?? true, hasPassword: false, password: undefined }]; tick().then(() => document.getElementById(`sv-host-${f.servers.length - 1}`)?.focus()) }
  function moveServer(i, d) { const s = [...f.servers]; [s[i], s[i + d]] = [s[i + d], s[i]]; f.servers = s }
  function removeServer(i) { f.servers = f.servers.filter((_, j) => j !== i) }
  function setTLS(sv, on) { sv.tls = on; if (sv.port === '6697' || sv.port === '6667') sv.port = on ? '6697' : '6667' }

  // ---- channels
  function addChannels() {
    const names = splitChannels(newChan)
    const bad = names.find((c) => !/^[#&+!][^\s,]{1,49}$/.test(c))
    if (bad) { errs.newChan = `“${bad}” isn’t a channel — channels start with #.`; return }
    errs.newChan = ''
    for (const c of names) if (!f.channels.some((x) => x.name.toLowerCase() === c.toLowerCase())) f.channels.push({ name: c, key: '', detached: false, disabled: false })
    newChan = ''
  }

  // ---- fingerprints
  const normFp = (s) => s.replace(/[:\s]/g, '').toLowerCase()
  const colon = (h) => h.toUpperCase().match(/.{1,2}/g)?.join(':') || ''
  function addFp(v = newFp) {
    const fp = normFp(v)
    if (!/^[0-9a-f]{64}$/.test(fp)) { errs.newFp = 'A SHA-256 fingerprint is 64 hex digits (colons are fine).'; return }
    errs.newFp = ''
    if (!f.fingerprints.includes(fp)) f.fingerprints = [...f.fingerprints, fp]
    newFp = ''
  }
  async function trustSeen() {
    addFp(net.untrustedFingerprint)
    await save(null, 'Trusted that certificate')
  }

  // ---- validation + save
  function validate() {
    const e = {}
    e.name = check('netname', f.name)
    f.servers.forEach((sv, i) => {
      e[`sv-host-${i}`] = check('host', sv.host)
      e[`sv-port-${i}`] = check('port', sv.port)
    })
    if (!f.servers.length) e.servers = 'Add at least one server.'
    e.nick = check('nick', f.nick)
    e.altNick = check('optnick', f.altNick)
    e.username = /[\s@!:]/.test(f.username) ? 'The ident can’t contain spaces, @, ! or :.' : ''
    if (f.floodOn) {
      e.floodRate = +f.floodRate > 0 && +f.floodRate <= 100 ? '' : 'Lines per second, more than 0 and up to 100.'
      e.floodBurst = Number.isInteger(+f.floodBurst) && +f.floodBurst >= 1 && +f.floodBurst <= 1000 ? '' : 'Whole number of lines, 1–1000.'
    }
    e.joinDelay = Number.isInteger(+f.joinDelay) && +f.joinDelay >= 0 && +f.joinDelay <= 600 ? '' : 'Seconds, 0–600.'
    errs = Object.fromEntries(Object.entries(e).filter(([, v]) => v))
    return !Object.keys(errs).length
  }
  function body() {
    const chanSettings = {}
    for (const c of f.channels) if (c.key || c.detached || c.disabled) chanSettings[c.name.toLowerCase()] = { key: c.key || undefined, detached: c.detached || undefined, disabled: c.disabled || undefined }
    const b = {
      name: f.name.trim(),
      servers: f.servers.map((sv) => ({ host: sv.host.trim(), port: +sv.port, tls: sv.tls, ...(sv.password !== undefined ? { password: sv.password } : {}) })),
      nick: f.nick.trim(), altNick: f.altNick.trim(), username: f.username.trim(), realname: f.realname.trim(), quitMsg: f.quitMsg.trim(),
      saslUser: f.saslUser.trim(),
      trustPKI: f.trustPKI, trustAllCerts: f.trustAllCerts, fingerprints: f.fingerprints,
      encoding: f.encodingName ? (f.encodingAlways ? '' : '^') + f.encodingName : '',
      floodRate: f.floodOn ? +f.floodRate : 0, floodBurst: +f.floodBurst || 9, joinDelay: +f.joinDelay || 0,
      channels: f.channels.map((c) => c.name), channelSettings: chanSettings,
      modules: f.modules || {},
    }
    if (!binds.locked) b.bindHost = f.bindHost.trim()
    if (f.password !== undefined) b.password = f.password
    return b
  }
  async function save(e, msg) {
    e?.preventDefault()
    formErr = ''
    if (!validate()) {
      await tick()
      document.querySelector('[aria-invalid="true"]')?.focus()
      return
    }
    saving = true
    try {
      if (id == null) {
        const n = await api.addNetwork(body())
        if (f.enabled) await api.connect(n.id).catch(() => {})
        toast(`Added ${n.name}${f.enabled ? ' — connecting…' : ''}`)
        refreshNets(); refreshMe()
        goto(`/networks/${n.id}`, { replaceState: true })
        return
      }
      const n = await api.updateNetwork(id, body())
      net = n; f = fromNet(n)
      toast(msg || (n.reconnected ? `Saved ${n.name} — reconnecting to apply` : `Saved ${n.name}`))
      refreshNets()
    } catch (err) {
      formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.'
      await tick(); document.getElementById('form-err')?.scrollIntoView({ block: 'center' })
    } finally { saving = false }
  }
  async function toggleConnect(on) {
    try {
      if (on) { await api.connect(id); toast(`Connecting to ${net.name}…`) } else { await api.disconnect(id); toast(`Disconnected from ${net.name}. Settings kept.`) }
      net = await api.network(id); f.enabled = net.enabled; refreshNets()
    } catch (e) { toast(e.message, 'bad') }
  }
  async function remove() {
    if (!(await confirmDialog({ title: `Remove ${net.name}?`, body: 'evrd disconnects, deletes its settings, and deletes this network’s chat history. It can’t be undone.', action: 'Remove network', typed: net.name }))) return
    try { await api.removeNetwork(id); toast(`Removed ${net.name}`); refreshNets(); refreshMe(); goto('/networks') } catch (e) { toast(e.message, 'bad') }
  }
  const pwState = (has, v) => (v === '' ? 'cleared' : v !== undefined ? 'new' : has ? 'saved' : 'none')
</script>

<svelte:head><title>{id == null ? 'Add a network' : net ? `${net.name} settings` : 'Network'} · evrd</title></svelte:head>

{#if loadErr}
  <div class="callout bad" role="alert"><Icon name="alert" /><span>{loadErr} <a href="/networks">Back to networks</a></span></div>
{:else if !f}
  <div class="skel" style="height:120px"></div><div class="skel" style="height:320px;margin-top:16px"></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/networks"><Icon name="chevron" class="i-sm flip" />Networks</a>
      <h1>{id == null ? 'Add a network' : net.name}</h1>
      <p>{id == null ? 'evrd connects as soon as you save, and stays connected.' : `${net.servers?.length || 1} server${(net.servers?.length || 1) === 1 ? '' : 's'} · ${net.currentNick || net.nick}${net.server ? ' · on ' + net.server : ''}`}</p>
    </div>
    {#if net}
      <div class="od-cluster">
        <StatusChip status={state.status} />
        <a class="btn" href="/chat?net={net.id}"><Icon name="message" />Open chat</a>
      </div>
    {/if}
  </div>

  <div class="editor">
    <nav class="toc" aria-label="Sections">
      {#each SECTIONS as [k, l]}<a href="#{k}">{l}</a>{/each}
      {#if net}<a href="#log">Connection log</a><a href="#danger">Remove</a>{/if}
    </nav>

    <form class="form sections" novalidate onsubmit={save} aria-label={id == null ? 'Add a network' : `Edit ${net.name}`}>
      {#if formErr}<div class="callout bad" role="alert" id="form-err"><Icon name="alert" /><span>{formErr}</span></div>{/if}
      {#if state && state.status !== 'connected' && state.status !== 'disconnected' && state.lastError && !state.untrustedFingerprint}
        <div class="callout bad" role="status" data-testid="conn-problem">
          <Icon name="alert" />
          <div class="od-stack" style="--od-gap:6px">
            <span><b>Can’t connect{state.server ? ' to ' + state.server : ''}:</b> {state.lastError}.</span>
            {#if state.hint}<span>{state.hint}</span>{/if}
            <span class="faint" style="font-size:var(--fs-xs)">{state.retryIn ? `Trying again in ${state.retryIn}s` : 'Trying again'}{state.attempts > 1 ? ` · attempt ${state.attempts}` : ''} · <a href="#log">connection log</a></span>
          </div>
        </div>
      {/if}
      {#if net?.untrustedFingerprint && net.status !== 'connected'}
        <div class="callout warn" role="alert">
          <Icon name="shield" />
          <div class="od-stack" style="--od-gap:8px">
            <span><b>evrd refused {net.server || 'the server'}’s certificate.</b> {net.lastError}</span>
            <span class="mono fp">{colon(net.untrustedFingerprint)}</span>
            <span>If you know this is the right server (a self-signed certificate, say), trust this exact certificate.</span>
            <div><button class="btn btn-sm" type="button" onclick={trustSeen}><Icon name="check" />Trust this certificate</button></div>
          </div>
        </div>
      {/if}

      {#if id == null && !manual}
        <DirectoryPicker picked={picked?.id} onpick={pick} onmanual={enterManually} />
      {/if}
      {#if id == null && picked}
        <div class="callout" role="status" data-testid="directory-picked">
          <Icon name="check" />
          <span>
            <b>{picked.name}</b>: its servers{picked.channels?.length ? ' and channels' : ''} are filled in below. Check your nickname under <a href="#identity">Identity</a>, then add it.
            {#if loginHint(picked)}{loginHint(picked)}{/if}
            {#if picked.homepage}<a href={picked.homepage} target="_blank" rel="noopener noreferrer">About {picked.name}</a>{/if}
          </span>
        </div>
      {/if}

      <section class="card" id="general" aria-labelledby="general-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="general-h">Network</h2><p class="desc">The name is how clients pick it: <span class="code">{me.username}/{f.name || 'name'}:password</span>.</p></div>
          <div class="form-grid">
            <Field id="name" label="Name" required err={errs.name}><input class="input" id="name" placeholder="libera" autocomplete="off" bind:value={f.name} aria-invalid={!!errs.name} /></Field>
            <div class="check-row">
              <span class="od-field"><span id="conn-l">Connected</span><span>Off keeps the settings without connecting.</span></span>
              <Switch checked={f.enabled} labelledby="conn-l" onchange={(v) => (id == null ? (f.enabled = v) : toggleConnect(v))} />
            </div>
          </div>
        </div>
      </section>

      <section class="card" id="servers" aria-labelledby="servers-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="servers-h">Servers</h2><p class="desc">evrd connects to the first one. When the link drops it moves on to the next, round the list — like ZNC.</p></div>
          <div class="od-stack" style="--od-gap:12px">
            {#if errs.servers}<p class="err-text">{errs.servers}</p>{/if}
            {#each f.servers as sv, i}
              <div class="server-row" data-testid="server-row">
                <span class="idx mono">{i + 1}</span>
                <Field id="sv-host-{i}" label="Server" err={errs[`sv-host-${i}`]}><input class="input mono" id="sv-host-{i}" placeholder="irc.libera.chat" spellcheck="false" autocomplete="off" bind:value={sv.host} aria-invalid={!!errs[`sv-host-${i}`]} /></Field>
                <Field id="sv-port-{i}" label="Port" err={errs[`sv-port-${i}`]}><input class="input mono" id="sv-port-{i}" inputmode="numeric" bind:value={sv.port} aria-invalid={!!errs[`sv-port-${i}`]} /></Field>
                <div class="tls"><span class="label" id="sv-tls-l-{i}">TLS</span><Switch checked={sv.tls} labelledby="sv-tls-l-{i}" onchange={(v) => setTLS(sv, v)} /></div>
                <Field id="sv-pass-{i}" label="Server password" help={pwState(sv.hasPassword, sv.password) === 'saved' ? 'Saved — type to replace.' : pwState(sv.hasPassword, sv.password) === 'cleared' ? 'Will be removed.' : 'Sent as PASS. Usually empty.'}>
                  <div class="input-group">
                    <input class="input mono" id="sv-pass-{i}" type="password" autocomplete="new-password" placeholder={sv.hasPassword && sv.password === undefined ? '•••••••• (unchanged)' : ''} value={sv.password ?? ''} oninput={(e) => (sv.password = e.currentTarget.value)} />
                    {#if sv.hasPassword && sv.password !== ''}<button class="btn btn-ghost btn-sm" type="button" onclick={() => (sv.password = '')}>Clear</button>{/if}
                  </div>
                </Field>
                <div class="actions">
                  <button class="icon-btn" type="button" aria-label="Move server {i + 1} up" disabled={i === 0} onclick={() => moveServer(i, -1)}><Icon name="chevronDown" class="flip-v" /></button>
                  <button class="icon-btn" type="button" aria-label="Move server {i + 1} down" disabled={i === f.servers.length - 1} onclick={() => moveServer(i, 1)}><Icon name="chevronDown" /></button>
                  <button class="icon-btn" type="button" aria-label="Remove server {i + 1}" disabled={f.servers.length === 1} onclick={() => removeServer(i)}><Icon name="trash" /></button>
                </div>
              </div>
            {/each}
            <div><button class="btn btn-sm" type="button" onclick={addServer}><Icon name="plus" />Add a server</button></div>
          </div>
        </div>
      </section>

      <section class="card" id="identity" aria-labelledby="identity-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="identity-h">Identity</h2><p class="desc">Who you are on this network. Empty fields use your <a href="/account#bouncer">account defaults</a>.</p></div>
          <div class="form-grid">
            <Field id="nick" label="Nickname" required err={errs.nick}><input class="input mono" id="nick" spellcheck="false" autocomplete="off" bind:value={f.nick} aria-invalid={!!errs.nick} /></Field>
            <Field id="altNick" label="Alternate nickname" help="Tried when the nickname is taken." err={errs.altNick}><input class="input mono" id="altNick" spellcheck="false" autocomplete="off" placeholder={me.defaultAltNick || `${f.nick || 'nick'}_`} bind:value={f.altNick} aria-invalid={!!errs.altNick} /></Field>
            <Field id="username" label="Ident" help="The user part of nick!ident@host." err={errs.username}><input class="input mono" id="username" spellcheck="false" autocomplete="off" placeholder={settings.defaultIdent || f.nick || 'same as nick'} bind:value={f.username} aria-invalid={!!errs.username} /></Field>
            <Field id="realname" label="Real name" help="Shown in /whois."><input class="input" id="realname" placeholder={me.defaultRealname || f.nick} bind:value={f.realname} /></Field>
            <Field id="quitMsg" label="Quit message" help="Sent when you switch the network off." span><input class="input" id="quitMsg" placeholder={settings.quitMsg || 'none'} bind:value={f.quitMsg} /></Field>
            <Field id="bindHost" label="Bind host" span help={binds.locked ? 'Set by your admin.' : binds.any ? 'The local address to connect from. Admins can use any address this server has.' : binds.hosts.length ? 'The local address evrd connects from.' : 'This bouncer doesn’t offer a choice of address.'}>
              {#if binds.any}
                <input class="input mono" id="bindHost" list="bindhost-list" placeholder={settings.bindHost || 'server default'} bind:value={f.bindHost} />
                <datalist id="bindhost-list">{#each binds.hosts as h}<option value={h}></option>{/each}</datalist>
              {:else}
                <Select id="bindHost" bind:value={f.bindHost} disabled={binds.locked || !binds.hosts.length} options={[
                  { value: '', label: settings.bindHost ? `Account default (${settings.bindHost})` : 'Server default' },
                  ...binds.hosts.map((h) => ({ value: h, label: h })),
                  ...(f.bindHost && !binds.hosts.includes(f.bindHost) ? [{ value: f.bindHost, label: f.bindHost }] : []),
                ]} />
              {/if}
            </Field>
          </div>
        </div>
      </section>

      <section class="card" id="sasl" aria-labelledby="sasl-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="sasl-h">Authentication</h2><p class="desc">SASL logs you in to your account (NickServ) while connecting. If the network has no SASL, evrd sends the password as PASS instead.</p></div>
          <div class="form-grid">
            <Field id="saslUser" label="Account name" help="Usually your registered nick."><input class="input mono" id="saslUser" spellcheck="false" autocomplete="off" placeholder={f.username || f.nick || 'nick'} bind:value={f.saslUser} /></Field>
            <Field id="saslPass" label="Password" help={pwState(f.hasPassword, f.password) === 'saved' ? 'A password is saved. Type a new one to replace it.' : pwState(f.hasPassword, f.password) === 'cleared' ? 'Will be removed: evrd connects without SASL.' : 'Leave empty if you don’t use SASL.'}>
              <div class="input-group">
                <input class="input mono" id="saslPass" type="password" autocomplete="new-password" placeholder={f.hasPassword && f.password === undefined ? '•••••••• (unchanged)' : ''} value={f.password ?? ''} oninput={(e) => (f.password = e.currentTarget.value)} />
                {#if f.hasPassword && f.password !== ''}<button class="btn btn-ghost btn-sm" type="button" onclick={() => (f.password = '')}>Clear</button>{/if}
              </div>
            </Field>
          </div>
        </div>
      </section>

      <section class="card" id="tls" aria-labelledby="tls-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="tls-h">Certificates</h2><p class="desc">How evrd decides to trust a TLS server. A listed fingerprint always wins.</p></div>
          <div class="od-stack" style="--od-gap:4px">
            <div class="check-row"><span class="od-field"><span id="pki-l">Verify with certificate authorities</span><span>Accept certificates a public CA signed for the server’s name (TrustPKI).</span></span><Switch checked={f.trustPKI} labelledby="pki-l" onchange={(v) => (f.trustPKI = v)} /></div>
            <div class="check-row"><span class="od-field"><span id="all-l">Trust every certificate</span><span>Skips all checks (TrustAllCerts). Anyone in the middle could read your traffic.</span></span><Switch checked={f.trustAllCerts} labelledby="all-l" onchange={(v) => (f.trustAllCerts = v)} /></div>
            {#if f.trustAllCerts}<div class="callout warn"><Icon name="alert" /><span>Certificates aren’t checked for this network. Prefer trusting the server’s fingerprint below.</span></div>{/if}
            <span class="caps" style="margin-top:var(--s3)">Trusted fingerprints (SHA-256)</span>
            <div class="rows">
              {#each f.fingerprints as fp}
                <div class="row fp-row"><span class="mono fp">{colon(fp)}</span><button class="btn btn-ghost btn-sm" type="button" aria-label="Remove fingerprint {colon(fp).slice(0, 11)}" onclick={() => (f.fingerprints = f.fingerprints.filter((x) => x !== fp))}>Remove</button></div>
              {:else}
                <p class="desc" style="font-size:var(--fs-xs)">None. For a self-signed server, add its certificate’s SHA-256 here — evrd shows it when it refuses one.</p>
              {/each}
            </div>
            <Field id="newFp" label="Add a fingerprint" err={errs.newFp}>
              <div class="input-group">
                <input class="input mono" id="newFp" placeholder="AB:CD:…  (64 hex digits)" spellcheck="false" autocomplete="off" bind:value={newFp} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFp() } }} />
                <button class="btn" type="button" onclick={() => addFp()}><Icon name="plus" />Add</button>
              </div>
            </Field>
          </div>
        </div>
      </section>

      <section class="card" id="channels" aria-labelledby="channels-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="channels-h">Channels</h2><p class="desc">Joined on every connect. /join and /part keep this list up to date. Detached channels stay joined and logged but are hidden from IRC clients until you /join them.</p></div>
          <div class="od-stack" style="--od-gap:12px">
            {#if f.channels.length}
              <div class="table-wrap">
                <table class="chan-table">
                  <thead><tr><th scope="col">Channel</th><th scope="col">Key</th><th scope="col">Detached</th><th scope="col">Join on connect</th><th scope="col"><span class="sr-only">Remove</span></th></tr></thead>
                  <tbody>
                    {#each f.channels as c (c.name)}
                      <tr data-testid="chan-row">
                        <td class="mono">{c.name}</td>
                        <td><input class="input mono key" aria-label="Key for {c.name}" placeholder="none" autocomplete="off" bind:value={c.key} /></td>
                        <td><Switch checked={c.detached} label="Detach {c.name}" onchange={(v) => (c.detached = v)} /></td>
                        <td><Switch checked={!c.disabled} label="Join {c.name} on connect" onchange={(v) => (c.disabled = !v)} /></td>
                        <td><button class="icon-btn" type="button" aria-label="Remove {c.name}" onclick={() => (f.channels = f.channels.filter((x) => x !== c))}><Icon name="trash" /></button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else}
              <p class="desc">No channels yet.</p>
            {/if}
            <Field id="newChan" label="Add channels" help="Separate with commas or spaces." err={errs.newChan}>
              <div class="input-group">
                <input class="input mono" id="newChan" placeholder="#evrd, #go-nuts" spellcheck="false" autocomplete="off" bind:value={newChan} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addChannels() } }} />
                <button class="btn" type="button" onclick={addChannels}><Icon name="plus" />Add</button>
              </div>
            </Field>
          </div>
        </div>
      </section>

      <section class="card" id="modules" aria-labelledby="modules-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="modules-h">Modules</h2><p class="desc">Extra behaviour for this network, like ZNC’s modules. Changes apply when you save — without reconnecting; perform’s commands run at the next sign-in.</p></div>
          <div class="mod-list">
            {#each available as m (m.name)}
              {@const on = !!f.modules[m.name]?.enabled}
              <div class="mod" class:on data-testid="module-{m.name}">
                <div class="check-row">
                  <span class="od-field"><span id="mod-{m.name}-l">{m.title}{#if m.znc}<span class="chip" style="margin-left:8px">ZNC {m.znc}</span>{/if}</span><span>{m.description}</span></span>
                  {#if m.off}<span class="chip">Switched off by an admin</span>
                  {:else}<Switch checked={on} labelledby="mod-{m.name}-l" onchange={(v) => setMod(m.name, v)} />{/if}
                </div>
                {#if on && !m.off && m.settings?.length}
                  <div class="mod-settings">
                    {#each m.settings as st (st.key)}
                      {@const fid = `mod-${m.name}-${st.key}`}
                      {#if st.kind === 'lines'}
                        <Field id={fid} label={st.label} help={st.help}><textarea class="input mono" id={fid} rows="4" value={val(m, st)} oninput={(e) => setVal(m, st, e.currentTarget.value)}></textarea></Field>
                      {:else if st.kind === 'bool'}
                        <div class="check-row"><span class="od-field"><span id="{fid}-l">{st.label}</span>{#if st.help}<span>{st.help}</span>{/if}</span><Switch checked={['yes', 'true', 'on', '1'].includes(String(val(m, st)).toLowerCase())} labelledby="{fid}-l" onchange={(v) => setVal(m, st, v ? 'yes' : 'no')} /></div>
                      {:else}
                        <Field id={fid} label={st.label} help={st.help}><input class="input mono" id={fid} inputmode={st.kind === 'number' ? 'numeric' : 'text'} value={val(m, st)} oninput={(e) => setVal(m, st, e.currentTarget.value)} /></Field>
                      {/if}
                    {/each}
                  </div>
                {/if}
              </div>
            {:else}
              <p class="desc">Loading modules…</p>
            {/each}
          </div>
        </div>
      </section>

      <section class="card" id="advanced" aria-labelledby="advanced-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="advanced-h">Advanced</h2><p class="desc">Character set for networks that don’t use UTF-8, and how fast evrd may send.</p></div>
          <div class="form-grid">
            <Field id="encoding" label="Encoding">
              <Select id="encoding" bind:value={f.encodingName} options={[{ value: '', label: 'UTF-8' }, ...ENCODINGS.map((e) => ({ value: e, label: e }))]} />
            </Field>
            {#if f.encodingName}
              <Field id="encMode" label="Use it">
                <Select id="encMode" value={f.encodingAlways ? 'always' : 'fallback'} onchange={(v) => (f.encodingAlways = v === 'always')} options={[
                  { value: 'fallback', label: 'Only for text that isn’t UTF-8 (send UTF-8)' },
                  { value: 'always', label: `For everything (send in ${f.encodingName})` },
                ]} />
              </Field>
            {:else}<span></span>{/if}
            <div class="check-row span-2"><span class="od-field"><span id="flood-l">Flood protection</span><span>Sends a burst, then paces lines so the server doesn’t disconnect you for flooding.</span></span><Switch checked={f.floodOn} labelledby="flood-l" onchange={(v) => (f.floodOn = v)} /></div>
            {#if f.floodOn}
              <Field id="floodRate" label="Lines per second" err={errs.floodRate}><input class="input mono" id="floodRate" inputmode="decimal" bind:value={f.floodRate} aria-invalid={!!errs.floodRate} /></Field>
              <Field id="floodBurst" label="Burst" help="Lines sent at once before pacing starts." err={errs.floodBurst}><input class="input mono" id="floodBurst" inputmode="numeric" bind:value={f.floodBurst} aria-invalid={!!errs.floodBurst} /></Field>
            {/if}
            <Field id="joinDelay" label="Join delay" help="Seconds to wait after connecting before joining (time for NickServ, cloaks…)." err={errs.joinDelay}><input class="input mono" id="joinDelay" inputmode="numeric" bind:value={f.joinDelay} aria-invalid={!!errs.joinDelay} /></Field>
          </div>
        </div>
      </section>

      {#if net}
        <section class="card" id="log" aria-labelledby="log-h">
          <div class="section-head" style="margin-bottom:var(--s3)">
            <div class="od-stack"><h2 id="log-h">Connection log</h2><p class="desc">What evrd did and what the server said while connecting — newest last. What evrd sends isn’t shown (it can carry passwords).</p></div>
            {#if log?.length}<button class="btn btn-ghost btn-sm" type="button" onclick={copyLog}><Icon name="copy" />Copy</button>{/if}
          </div>
          {#if !log}
            <div class="skel" style="height:80px"></div>
          {:else if !log.length}
            <p class="desc" style="font-size:var(--fs-sm)">Nothing yet: entries appear when evrd connects this network.</p>
          {:else}
            <ConnLog entries={log} />
          {/if}
        </section>

        <section class="card danger-card" id="danger" aria-labelledby="danger-h">
          <div class="panel-grid">
            <div class="od-stack"><h2 id="danger-h">Remove network</h2><p class="desc">Disconnects and deletes its settings and chat history.</p></div>
            <div><button class="btn btn-danger" type="button" onclick={remove}><Icon name="trash" />Remove {net.name}</button></div>
          </div>
        </section>
      {/if}

      <div class="save-bar">
        <a class="btn btn-ghost" href="/networks">Cancel</a>
        <button class="btn btn-primary" type="submit" disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />{id == null ? 'Add network' : 'Save changes'}{/if}</button>
      </div>
    </form>
  </div>
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--muted); }
  .mod-list { display: grid; gap: var(--s3); }
  .mod { padding: var(--s3) var(--s4); border-radius: var(--r-row); border: 1px solid var(--line-soft); background: var(--bg-deep); display: grid; gap: var(--s3); }
  .mod.on { border-color: var(--line-2); }
  .mod-settings { display: grid; gap: var(--s3); padding-top: var(--s2); border-top: 1px solid var(--line-soft); }
  :global(.flip) { transform: rotate(180deg); }
  :global(.flip-v) { transform: rotate(180deg); }
  .editor { display: grid; grid-template-columns: 180px minmax(0, 1fr); gap: var(--s6); align-items: start; margin-top: var(--s6); }
  .toc { position: sticky; top: calc(var(--topbar-h) + var(--s5)); display: grid; gap: 2px; }
  .toc a { padding: 8px 12px; border-radius: var(--r-field); color: var(--muted); font-size: var(--fs-sm); }
  .toc a:hover { background: var(--row-hover); color: var(--fg); text-decoration: none; }
  .sections { display: grid; gap: var(--s5); }
  .sections > section { scroll-margin-top: calc(var(--topbar-h) + var(--s4)); }
  .panel-grid { display: grid; grid-template-columns: minmax(0, 240px) minmax(0, 1fr); gap: var(--s7); }
  .panel-grid :global(.od-stack) { --od-gap: 6px; }
  /* host · port · TLS · actions on one line, the server password below */
  .server-row { display: grid; grid-template-columns: 24px minmax(0, 1fr) 96px auto auto; gap: var(--s3); align-items: start; padding: var(--s3); border-radius: var(--r-row); background: var(--row); }
  .server-row > :global(:nth-child(5)) { grid-column: 2 / 5; grid-row: 2; }
  .server-row .idx { padding-top: 38px; color: var(--faint); font-size: var(--fs-xs); text-align: center; }
  .server-row .tls { display: grid; gap: 10px; justify-items: start; }
  .server-row .tls .label { font-size: var(--fs-sm); font-weight: 500; }
  .server-row .actions { display: flex; gap: 2px; padding-top: 28px; grid-column: 5; grid-row: 1; }
  .fp { font-size: var(--fs-xs); word-break: break-all; color: var(--fg); }
  .fp-row { --cols: minmax(0, 1fr) auto; }
  .chan-table td { vertical-align: middle; }
  .chan-table .key { height: 36px; max-width: 160px; }
  .err-text { color: var(--bad); font-size: var(--fs-xs); }
  .danger-card { border-color: oklch(68% 0.18 27 / 0.35); }
  .save-bar { position: sticky; bottom: 0; z-index: 5; display: flex; justify-content: flex-end; gap: var(--s2); padding: var(--s3) var(--s4); border-radius: var(--r-card); background: var(--glass-strong, var(--bg-2)); border: 1px solid var(--line); backdrop-filter: blur(12px); }
  :global(.callout.warn) { background: oklch(80% 0.14 75 / 0.1); border-color: oklch(80% 0.14 75 / 0.35); color: var(--fg); }
  :global(.callout.warn .i) { color: var(--warn); }
  @media (max-width: 1099px) {
    .editor { grid-template-columns: minmax(0, 1fr); }
    .toc { position: static; display: flex; flex-wrap: wrap; }
    .server-row { grid-template-columns: 20px minmax(0, 1fr) 88px; }
    .server-row > :global(:nth-child(4)) { grid-column: 2 / -1; grid-row: 2; }
    .server-row > :global(:nth-child(5)) { grid-column: 2 / -1; grid-row: 3; }
    .server-row .actions { grid-column: 2 / -1; grid-row: 4; padding-top: 0; }
  }
  @media (max-width: 959px) { .panel-grid { grid-template-columns: minmax(0, 1fr); gap: var(--s4); } }
</style>
