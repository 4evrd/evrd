<script>
  // The full network settings page, ZNC-style: servers (tried in turn),
  // identity, services login, certificate trust, channels and the advanced
  // knobs.
  // id = null adds a network.
  import { onMount, onDestroy, tick } from 'svelte'
  import { goto } from '$app/navigation'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Select from './Select.svelte'
  import Switch from './Switch.svelte'
  import StatusChip from './StatusChip.svelte'
  import Copy from './Copy.svelte'
  import ConnLog from './ConnLog.svelte'
  import DirectoryPicker from './DirectoryPicker.svelte'
  import Msg from './Msg.svelte'
  import PlaybackSettings from './PlaybackSettings.svelte'
  import { api } from '$lib/api.js'
  import { wait } from '$lib/format.js'
  import { fromDirectory, loginHint } from '$lib/directory.js'
  import { session, refreshNets, refreshMe, ircPort } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { check, splitChannels } from '$lib/validate.js'
  import { METHODS, PRESETS, presetOf, applyPreset, usesPassword, checkAuth, authLabel } from '$lib/services.js'
  import { t } from '$lib/i18n.svelte.js'

  let { id = null } = $props()
  const me = $derived(session.me)
  const settings = $derived(me.settings || {})

  const ENCODINGS = ['ISO-8859-1', 'ISO-8859-15', 'windows-1252', 'ISO-8859-2', 'windows-1250', 'windows-1251', 'KOI8-R', 'ISO-8859-7', 'ISO-8859-9', 'Shift_JIS', 'EUC-JP', 'ISO-2022-JP', 'GBK', 'Big5', 'EUC-KR']
  const SECTIONS = ['servers', 'identity', 'sasl', 'tls', 'channels', 'modules', 'advanced']

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
    try { await navigator.clipboard.writeText(text); toast(t('networks.logCopied')) } catch { toast(t('networks.copyFailed'), 'bad') }
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
      authMethod: n.authMethod || 'auto', servicesNick: n.servicesNick || '', identifyArgs: n.identifyArgs || '', identifyOn: n.identifyOn || '',
      ghost: n.ghost || '', authCommand: n.authCommand || '', joinAfterAuth: !!n.joinAfterAuth, authTimeout: String(n.authTimeout || 0),
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
      authMethod: 'auto', servicesNick: '', identifyArgs: '', identifyOn: '', ghost: '', authCommand: '', joinAfterAuth: false, authTimeout: '0',
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
    try { net = await api.network(id); f = fromNet(net) } catch (e) { loadErr = e.status === 404 ? t('networks.notFound') : e.message }
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
    if (bad) { errs.newChan = t('networks.badChannel', { channel: bad }); return }
    errs.newChan = ''
    for (const c of names) if (!f.channels.some((x) => x.name.toLowerCase() === c.toLowerCase())) f.channels.push({ name: c, key: '', detached: false, disabled: false })
    newChan = ''
  }

  // ---- fingerprints
  const normFp = (s) => s.replace(/[:\s]/g, '').toLowerCase()
  const colon = (h) => h.toUpperCase().match(/.{1,2}/g)?.join(':') || ''
  function addFp(v = newFp) {
    const fp = normFp(v)
    if (!/^[0-9a-f]{64}$/.test(fp)) { errs.newFp = t('networks.badFingerprint'); return }
    errs.newFp = ''
    if (!f.fingerprints.includes(fp)) f.fingerprints = [...f.fingerprints, fp]
    newFp = ''
  }
  async function trustSeen() {
    addFp(net.untrustedFingerprint)
    await save(null, t('networks.trustedCert'))
  }

  // ---- validation + save
  function validate() {
    const e = {}
    e.name = check('netname', f.name)
    f.servers.forEach((sv, i) => {
      e[`sv-host-${i}`] = check('host', sv.host)
      e[`sv-port-${i}`] = check('port', sv.port)
    })
    if (!f.servers.length) e.servers = t('networks.badServers')
    e.nick = check('nick', f.nick)
    e.altNick = check('optnick', f.altNick)
    e.username = /[\s@!:]/.test(f.username) ? t('networks.badIdent') : ''
    if (f.floodOn) {
      e.floodRate = +f.floodRate > 0 && +f.floodRate <= 100 ? '' : t('networks.badFloodRate')
      e.floodBurst = Number.isInteger(+f.floodBurst) && +f.floodBurst >= 1 && +f.floodBurst <= 1000 ? '' : t('networks.badFloodBurst')
    }
    e.joinDelay = Number.isInteger(+f.joinDelay) && +f.joinDelay >= 0 && +f.joinDelay <= 600 ? '' : t('networks.badJoinDelay')
    Object.assign(e, checkAuth(f, t))
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
      authMethod: f.authMethod, servicesNick: f.servicesNick.trim(), identifyArgs: f.identifyArgs, identifyOn: f.identifyOn,
      ghost: f.ghost, authCommand: f.authCommand.trim(), joinAfterAuth: f.joinAfterAuth, authTimeout: +f.authTimeout || 0,
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
        toast(t(f.enabled ? 'networks.addedConnecting' : 'networks.added', { name: n.name }))
        refreshNets(); refreshMe()
        goto(`/networks/${n.id}`, { replaceState: true })
        return
      }
      const n = await api.updateNetwork(id, body())
      net = n; f = fromNet(n)
      toast(msg || t(n.reconnected ? 'networks.savedReconnecting' : 'networks.saved', { name: n.name }))
      refreshNets()
    } catch (err) {
      formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.'
      await tick(); document.getElementById('form-err')?.scrollIntoView({ block: 'center' })
    } finally { saving = false }
  }
  async function toggleConnect(on) {
    try {
      if (on) { await api.connect(id); toast(t('networks.connecting', { name: net.name })) } else { await api.disconnect(id); toast(t('networks.disconnected', { name: net.name })) }
      net = await api.network(id); f.enabled = net.enabled; refreshNets()
    } catch (e) { toast(e.message, 'bad') }
  }
  async function remove() {
    if (!(await confirmDialog({ title: t('networks.removeConfirm', { name: net.name }), body: t('networks.removeBody'), action: t('networks.removeNetwork'), typed: net.name }))) return
    try { await api.removeNetwork(id); toast(t('networks.removed', { name: net.name })); refreshNets(); refreshMe(); goto('/networks') } catch (e) { toast(e.message, 'bad') }
  }
  const pwState = (has, v) => (v === '' ? 'cleared' : v !== undefined ? 'new' : has ? 'saved' : 'none')
</script>

<svelte:head><title>{id == null ? t('networks.addANetwork') : net ? t('networks.pageTitle', { name: net.name }) : t('networks.network')} · evrd</title></svelte:head>

{#if loadErr}
  <div class="callout bad" role="alert"><Icon name="alert" /><span>{loadErr} <a href="/networks">{t('networks.backToNetworks')}</a></span></div>
{:else if !f}
  <div class="skel" style="height:120px"></div><div class="skel" style="height:320px;margin-top:16px"></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/networks"><Icon name="chevron" class="i-sm flip" />{t('networks.back')}</a>
      <h1>{id == null ? t('networks.addANetwork') : net.name}</h1>
      <p>{id == null ? t('networks.addIntro') : `${t('networks.serverCount', { count: net.servers?.length || 1 })} · ${net.currentNick || net.nick}${net.server ? ' · ' + t('networks.onServer', { server: net.server }) : ''}`}</p>
    </div>
    {#if net}
      <div class="od-cluster">
        <StatusChip status={state.status} />
        <a class="btn btn-ghost" href="/networks/{net.id}/health" title={t('networks.healthTip')}><Icon name="chart" />{t('networks.health')}</a>
        <a class="btn" href="/chat?net={net.id}"><Icon name="message" />{t('networks.openChat')}</a>
      </div>
    {/if}
  </div>

  <div class="editor">
    <nav class="toc" aria-label={t('networks.sections')}>
      {#if net}<a href="#connect">{t('networks.section.connect')}</a>{/if}
      {#each SECTIONS as k}<a href="#{k}">{t(`networks.section.${k}`)}</a>{#if k === 'channels' && net}<a href="#playback">{t('networks.section.playback')}</a>{/if}{/each}
      {#if net}<a href="#log">{t('networks.section.log')}</a><a href="#danger">{t('networks.section.remove')}</a>{/if}
    </nav>

    <form class="form sections" novalidate onsubmit={save} aria-label={id == null ? t('networks.formAdd') : t('networks.formEdit', { name: net.name })}>
      {#if formErr}<div class="callout bad" role="alert" id="form-err"><Icon name="alert" /><span>{formErr}</span></div>{/if}
      {#if state && state.status !== 'connected' && state.status !== 'disconnected' && state.lastError && !state.untrustedFingerprint}
        <div class="callout bad" role="status" data-testid="conn-problem">
          <Icon name="alert" />
          <div class="od-stack" style="--od-gap:6px">
            <span><b>{state.server ? t('networks.cantConnectTo', { server: state.server }) : t('networks.cantConnect')}</b> {state.lastError}.</span>
            {#if state.hint}<span>{state.hint}</span>{/if}
            <span class="faint" style="font-size:var(--fs-xs)">{state.retryIn ? t('networks.tryingAgainIn', { wait: wait(state.retryIn) }) : t('networks.tryingAgain')}{state.attempts > 1 ? ` · ${t('networks.attempt', { count: state.attempts })}` : ''} · <a href="#log">{t('networks.connectionLogLink')}</a></span>
          </div>
        </div>
      {/if}
      {#if net?.untrustedFingerprint && net.status !== 'connected'}
        <div class="callout warn" role="alert">
          <Icon name="shield" />
          <div class="od-stack" style="--od-gap:8px">
            <span><b>{t('networks.certRefused', { server: net.server || t('networks.theServer') })}</b> {net.lastError}</span>
            <span class="mono fp">{colon(net.untrustedFingerprint)}</span>
            <span>{t('networks.certTrustHelp')}</span>
            <div><button class="btn btn-sm" type="button" onclick={trustSeen}><Icon name="check" />{t('networks.trustCert')}</button></div>
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
            <Msg key={picked.channels?.length ? 'networks.pickedChannels' : 'networks.picked'}>{#snippet name()}<b>{picked.name}</b>{/snippet}{#snippet identity()}<a href="#identity">{t('networks.section.identity')}</a>{/snippet}</Msg>
            {#if loginHint(picked, t)}{loginHint(picked, t)}{/if}
            {#if picked.homepage}<a href={picked.homepage} target="_blank" rel="noopener noreferrer">{t('networks.about', { name: picked.name })}</a>{/if}
          </span>
        </div>
      {/if}

      {#if net}
        <section class="card" id="connect" aria-labelledby="connect-h" data-testid="connect-box">
          <div class="panel-grid">
            <div class="od-stack"><h2 id="connect-h">{t('networks.section.connect')}</h2><p class="desc">{t('networks.connectIntro', { name: net.name })}</p></div>
            <div class="kv-list">
              <div class="kv"><div class="od-field"><span class="k">{t('networks.server')}</span><span class="v mono">{session.config?.ircHost}</span></div><Copy text={session.config?.ircHost || ''} label={t('networks.copyServer')} /></div>
              <div class="kv"><div class="od-field"><span class="k">{t('networks.port')}</span><span class="v mono">{ircPort().port}{ircPort().tls ? ' · TLS' : ` · ${t('networks.noTls')}`}</span></div><Copy text={String(ircPort().port)} label={t('networks.copyPort')} /></div>
              <div class="kv"><div class="od-field"><span class="k">{t('networks.serverPassword')}</span><span class="v mono" data-testid="connect-pass">{me.username}/{net.name}:<i>{t('networks.passwordWord')}</i></span></div><Copy text="{me.username}/{net.name}:" label={t('networks.copyServerPassword')} /></div>
              <p class="desc" style="font-size:var(--fs-xs)">
                {#snippet pw()}<i>{t('networks.passwordWord')}</i>{/snippet}
                {#snippet clientPw()}<a href="/clients">{t('networks.clientPassword')}</a>{/snippet}
                {#if me.hasPassword}<Msg key="networks.passwordIsAccount" password={pw} clientPassword={clientPw} />
                {:else}<Msg key="networks.passwordIsClient" vars={{ sso: session.config?.ssoLabel || t('networks.sso'), ssoShort: session.config?.ssoLabel || t('networks.ssoShort') }} password={pw} clientPassword={clientPw} />{/if}
                {#if session.config?.ircSelfSigned && ircPort().tls} {t('networks.selfSigned')}{/if}
              </p>
            </div>
          </div>
        </section>
      {/if}

      <section class="card" id="general" aria-labelledby="general-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="general-h">{t('networks.network')}</h2><p class="desc"><Msg key="networks.nameIntro">{#snippet example()}<span class="code">{me.username}/{f.name || t('networks.nameWord')}:{t('networks.passwordWord')}</span>{/snippet}</Msg></p></div>
          <div class="form-grid">
            <Field id="name" label={t('networks.name')} required err={errs.name}><input class="input" id="name" placeholder="libera" autocomplete="off" bind:value={f.name} aria-invalid={!!errs.name} /></Field>
            <div class="check-row">
              <span class="od-field"><span id="conn-l">{t('networks.connected')}</span><span>{t('networks.connectedHelp')}</span></span>
              <Switch checked={f.enabled} labelledby="conn-l" onchange={(v) => (id == null ? (f.enabled = v) : toggleConnect(v))} />
            </div>
          </div>
        </div>
      </section>

      <section class="card" id="servers" aria-labelledby="servers-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="servers-h">{t('networks.section.servers')}</h2><p class="desc">{t('networks.serversIntro')}</p></div>
          <div class="od-stack" style="--od-gap:12px">
            {#if errs.servers}<p class="err-text">{errs.servers}</p>{/if}
            {#each f.servers as sv, i}
              <div class="server-row" data-testid="server-row">
                <span class="idx mono">{i + 1}</span>
                <Field id="sv-host-{i}" label={t('networks.server')} err={errs[`sv-host-${i}`]}><input class="input mono" id="sv-host-{i}" placeholder="irc.libera.chat" spellcheck="false" autocomplete="off" bind:value={sv.host} aria-invalid={!!errs[`sv-host-${i}`]} /></Field>
                <Field id="sv-port-{i}" label={t('networks.port')} err={errs[`sv-port-${i}`]}><input class="input mono" id="sv-port-{i}" inputmode="numeric" bind:value={sv.port} aria-invalid={!!errs[`sv-port-${i}`]} /></Field>
                <div class="tls"><span class="label" id="sv-tls-l-{i}">TLS</span><Switch checked={sv.tls} labelledby="sv-tls-l-{i}" onchange={(v) => setTLS(sv, v)} /></div>
                <Field id="sv-pass-{i}" label={t('networks.serverPassword')} encrypted help={pwState(sv.hasPassword, sv.password) === 'saved' ? t('networks.passSaved') : pwState(sv.hasPassword, sv.password) === 'cleared' ? t('networks.passRemoved') : t('networks.passHelp')}>
                  <div class="input-group">
                    <input class="input mono" id="sv-pass-{i}" type="password" autocomplete="new-password" placeholder={sv.hasPassword && sv.password === undefined ? t('networks.unchanged') : ''} value={sv.password ?? ''} oninput={(e) => (sv.password = e.currentTarget.value)} />
                    {#if sv.hasPassword && sv.password !== ''}<button class="btn btn-ghost btn-sm" type="button" onclick={() => (sv.password = '')}>{t('networks.clear')}</button>{/if}
                  </div>
                </Field>
                <div class="actions">
                  <button class="icon-btn" type="button" aria-label={t('networks.moveUp', { n: i + 1 })} disabled={i === 0} onclick={() => moveServer(i, -1)}><Icon name="chevronDown" class="flip-v" /></button>
                  <button class="icon-btn" type="button" aria-label={t('networks.moveDown', { n: i + 1 })} disabled={i === f.servers.length - 1} onclick={() => moveServer(i, 1)}><Icon name="chevronDown" /></button>
                  <button class="icon-btn" type="button" aria-label={t('networks.removeServer', { n: i + 1 })} disabled={f.servers.length === 1} onclick={() => removeServer(i)}><Icon name="trash" /></button>
                </div>
              </div>
            {/each}
            <div><button class="btn btn-sm" type="button" onclick={addServer}><Icon name="plus" />{t('networks.addServer')}</button></div>
          </div>
        </div>
      </section>

      <section class="card" id="identity" aria-labelledby="identity-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="identity-h">{t('networks.section.identity')}</h2><p class="desc"><Msg key="networks.identityIntro">{#snippet defaults()}<a href="/account#bouncer">{t('networks.accountDefaults')}</a>{/snippet}</Msg></p></div>
          <div class="form-grid">
            <Field id="nick" label={t('networks.nick')} required err={errs.nick}><input class="input mono" id="nick" spellcheck="false" autocomplete="off" bind:value={f.nick} aria-invalid={!!errs.nick} /></Field>
            <Field id="altNick" label={t('networks.altNick')} help={t('networks.altNickHelp')} err={errs.altNick}><input class="input mono" id="altNick" spellcheck="false" autocomplete="off" placeholder={me.defaultAltNick || `${f.nick || t('networks.nickWord')}_`} bind:value={f.altNick} aria-invalid={!!errs.altNick} /></Field>
            <Field id="username" label={t('networks.ident')} help={t('networks.identHelp')} err={errs.username}><input class="input mono" id="username" spellcheck="false" autocomplete="off" placeholder={settings.defaultIdent || f.nick || t('networks.sameAsNick')} bind:value={f.username} aria-invalid={!!errs.username} /></Field>
            <Field id="realname" label={t('networks.realname')} help={t('networks.realnameHelp')}><input class="input" id="realname" placeholder={me.defaultRealname || f.nick} bind:value={f.realname} /></Field>
            <Field id="quitMsg" label={t('networks.quitMsg')} help={t('networks.quitMsgHelp')} span><input class="input" id="quitMsg" placeholder={settings.quitMsg || t('networks.none')} bind:value={f.quitMsg} /></Field>
            <Field id="bindHost" label={t('networks.bindHost')} span help={binds.locked ? t('networks.bindLocked') : binds.any ? t('networks.bindAny') : binds.hosts.length ? t('networks.bindHosts') : t('networks.bindNone')}>
              {#if binds.any}
                <input class="input mono" id="bindHost" list="bindhost-list" placeholder={settings.bindHost || t('networks.serverDefaultLower')} bind:value={f.bindHost} />
                <datalist id="bindhost-list">{#each binds.hosts as h}<option value={h}></option>{/each}</datalist>
              {:else}
                <Select id="bindHost" bind:value={f.bindHost} disabled={binds.locked || !binds.hosts.length} options={[
                  { value: '', label: settings.bindHost ? t('networks.accountDefault', { host: settings.bindHost }) : t('networks.serverDefault') },
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
          <div class="od-stack">
            <h2 id="sasl-h">{t('networks.section.sasl')}</h2>
            <p class="desc">{t('networks.authIntro')} <a href="https://github.com/4evrd/evrd/blob/main/docs/services.md" target="_blank" rel="noreferrer">{t('networks.more')}</a></p>
            {#if state?.auth}<p class="desc" data-testid="auth-state"><span class="chip">{authLabel(state.auth, t)}</span></p>{/if}
          </div>
          <div class="form-grid">
            <Field id="authPreset" label={t('networks.authPreset')}>
              <Select id="authPreset" value={presetOf(f)} onchange={(v) => applyPreset(f, v)} options={PRESETS.map((p) => ({ value: p.key, label: t(p.label) }))} />
            </Field>
            <Field id="authMethod" label={t('networks.authMethod')}>
              <Select id="authMethod" bind:value={f.authMethod} options={METHODS.map(([k, l]) => ({ value: k, label: t(l) }))} />
            </Field>
            {#if usesPassword(f)}
              <Field id="saslUser" label={t('networks.account')} help={t('networks.accountHelp')}><input class="input mono" id="saslUser" spellcheck="false" autocomplete="off" placeholder={f.authMethod === 'sasl' || f.authMethod === 'auto' ? f.username || f.nick || t('networks.nickWord') : f.nick || t('networks.nickWord')} bind:value={f.saslUser} /></Field>
              <Field id="saslPass" label={t('networks.password')} encrypted help={pwState(f.hasPassword, f.password) === 'saved' ? t('networks.authPassSaved') : pwState(f.hasPassword, f.password) === 'cleared' ? t('networks.authPassRemoved') : t('networks.authPassHelp')}>
                <div class="input-group">
                  <input class="input mono" id="saslPass" type="password" autocomplete="new-password" placeholder={f.hasPassword && f.password === undefined ? t('networks.unchanged') : ''} value={f.password ?? ''} oninput={(e) => (f.password = e.currentTarget.value)} />
                  {#if f.hasPassword && f.password !== ''}<button class="btn btn-ghost btn-sm" type="button" onclick={() => (f.password = '')}>{t('networks.clear')}</button>{/if}
                </div>
              </Field>
            {/if}
            {#if f.authMethod === 'none' && f.modules?.q?.enabled}
              <p class="desc span-2">{t('networks.qModule')}</p>
            {/if}
            {#if f.authMethod === 'auto' || f.authMethod === 'nickserv'}
              <Field id="servicesNick" label={t('networks.servicesNick')} help={f.authMethod === 'auto' ? t('networks.servicesNickAuto') : t('networks.servicesNickHelp')} err={errs.servicesNick}><input class="input mono" id="servicesNick" spellcheck="false" autocomplete="off" placeholder="NickServ" bind:value={f.servicesNick} aria-invalid={!!errs.servicesNick} /></Field>
              <Field id="identifyArgs" label={t('networks.identifyArgs')}>
                <Select id="identifyArgs" bind:value={f.identifyArgs} options={[
                  { value: '', label: t('networks.identifyArgs.auto') },
                  { value: 'account', label: t('networks.identifyArgs.account') },
                  { value: 'password', label: t('networks.identifyArgs.password') },
                ]} />
              </Field>
              <Field id="identifyOn" label={t('networks.identifyOn')}>
                <Select id="identifyOn" bind:value={f.identifyOn} options={[
                  { value: '', label: t('networks.identifyOn.connect') },
                  { value: 'prompt', label: t('networks.identifyOn.prompt') },
                ]} />
              </Field>
            {/if}
            {#if f.authMethod === 'command'}
              <Field id="authCommand" label={t('networks.authCommand')} span encrypted err={errs.authCommand}>{#snippet helpBody()}<Msg key="networks.authCommandHelp">{#snippet account()}<span class="code">%account%</span>{/snippet}{#snippet password()}<span class="code">%password%</span>{/snippet}{#snippet nick()}<span class="code">%nick%</span>{/snippet}</Msg>{/snippet}<input class="input mono" id="authCommand" spellcheck="false" autocomplete="off" placeholder="PRIVMSG X@channels.undernet.org :LOGIN %account% %password%" bind:value={f.authCommand} aria-invalid={!!errs.authCommand} /></Field>
            {/if}
            {#if f.authMethod !== 'none'}
              <Field id="ghost" label={t('networks.ghost')} help={t('networks.ghostHelp')}>
                <Select id="ghost" bind:value={f.ghost} options={[
                  { value: '', label: t('networks.ghost.keep') },
                  { value: 'ghost', label: t('networks.ghost.ghost') },
                  { value: 'regain', label: t('networks.ghost.regain') },
                  { value: 'recover', label: t('networks.ghost.recover') },
                ]} />
              </Field>
              <Field id="authTimeout" label={t('networks.authTimeout')} help={t('networks.authTimeoutHelp')} err={errs.authTimeout}><input class="input mono" id="authTimeout" inputmode="numeric" bind:value={f.authTimeout} aria-invalid={!!errs.authTimeout} /></Field>
              <div class="check-row span-2"><span class="od-field"><span id="jaa-l">{t('networks.joinAfterAuth')}</span><span>{t('networks.joinAfterAuthHelp')}</span></span><Switch checked={f.joinAfterAuth} labelledby="jaa-l" onchange={(v) => (f.joinAfterAuth = v)} /></div>
            {/if}
          </div>
        </div>
      </section>

      <section class="card" id="tls" aria-labelledby="tls-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="tls-h">{t('networks.section.tls')}</h2><p class="desc">{t('networks.tlsIntro')}</p></div>
          <div class="od-stack" style="--od-gap:4px">
            <div class="check-row"><span class="od-field"><span id="pki-l">{t('networks.trustPKI')}</span><span>{t('networks.trustPKIHelp')}</span></span><Switch checked={f.trustPKI} labelledby="pki-l" onchange={(v) => (f.trustPKI = v)} /></div>
            <div class="check-row"><span class="od-field"><span id="all-l">{t('networks.trustAll')}</span><span>{t('networks.trustAllHelp')}</span></span><Switch checked={f.trustAllCerts} labelledby="all-l" onchange={(v) => (f.trustAllCerts = v)} /></div>
            {#if f.trustAllCerts}<div class="callout warn"><Icon name="alert" /><span>{t('networks.trustAllWarn')}</span></div>{/if}
            <span class="caps" style="margin-top:var(--s3)">{t('networks.fingerprints')}</span>
            <div class="rows">
              {#each f.fingerprints as fp}
                <div class="row fp-row"><span class="mono fp">{colon(fp)}</span><button class="btn btn-ghost btn-sm" type="button" aria-label={t('networks.removeFingerprint', { fp: colon(fp).slice(0, 11) })} onclick={() => (f.fingerprints = f.fingerprints.filter((x) => x !== fp))}>{t('networks.remove')}</button></div>
              {:else}
                <p class="desc" style="font-size:var(--fs-xs)">{t('networks.noFingerprints')}</p>
              {/each}
            </div>
            <Field id="newFp" label={t('networks.addFingerprint')} err={errs.newFp}>
              <div class="input-group">
                <input class="input mono" id="newFp" placeholder={t('networks.fpPlaceholder')} spellcheck="false" autocomplete="off" bind:value={newFp} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFp() } }} />
                <button class="btn" type="button" onclick={() => addFp()}><Icon name="plus" />{t('networks.add')}</button>
              </div>
            </Field>
          </div>
        </div>
      </section>

      <section class="card" id="channels" aria-labelledby="channels-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="channels-h">{t('networks.section.channels')}</h2><p class="desc">{t('networks.channelsIntro')}</p></div>
          <div class="od-stack" style="--od-gap:12px">
            {#if f.channels.length}
              <div class="table-wrap">
                <table class="chan-table">
                  <thead><tr><th scope="col">{t('networks.channel')}</th><th scope="col">{t('networks.key')}</th><th scope="col">{t('networks.detached')}</th><th scope="col">{t('networks.joinOnConnect')}</th><th scope="col"><span class="sr-only">{t('networks.remove')}</span></th></tr></thead>
                  <tbody>
                    {#each f.channels as c (c.name)}
                      <tr data-testid="chan-row">
                        <td class="mono">{c.name}</td>
                        <td><input class="input mono key" aria-label={t('networks.keyFor', { channel: c.name })} placeholder={t('networks.none')} autocomplete="off" bind:value={c.key} /></td>
                        <td><Switch checked={c.detached} label={t('networks.detach', { channel: c.name })} onchange={(v) => (c.detached = v)} /></td>
                        <td><Switch checked={!c.disabled} label={t('networks.joinChannel', { channel: c.name })} onchange={(v) => (c.disabled = !v)} /></td>
                        <td><button class="icon-btn" type="button" aria-label={t('networks.removeChannel', { channel: c.name })} onclick={() => (f.channels = f.channels.filter((x) => x !== c))}><Icon name="trash" /></button></td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else}
              <p class="desc">{t('networks.noChannels')}</p>
            {/if}
            <Field id="newChan" label={t('networks.addChannels')} help={t('networks.addChannelsHelp')} err={errs.newChan}>
              <div class="input-group">
                <input class="input mono" id="newChan" placeholder="#evrd, #go-nuts" spellcheck="false" autocomplete="off" bind:value={newChan} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addChannels() } }} />
                <button class="btn" type="button" onclick={addChannels}><Icon name="plus" />{t('networks.add')}</button>
              </div>
            </Field>
          </div>
        </div>
      </section>

      {#if net}<PlaybackSettings id={net.id} name={net.name} />{/if}

      <section class="card" id="modules" aria-labelledby="modules-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="modules-h">{t('networks.section.modules')}</h2><p class="desc">{t('networks.modulesIntro')}</p></div>
          <div class="mod-list">
            {#each available as m (m.name)}
              {@const on = !!f.modules[m.name]?.enabled}
              <div class="mod" class:on data-testid="module-{m.name}">
                <div class="check-row">
                  <span class="od-field"><span id="mod-{m.name}-l">{m.title}{#if m.znc}<span class="chip" style="margin-left:8px">ZNC {m.znc}</span>{/if}</span><span>{m.description}</span></span>
                  {#if m.off}<span class="chip">{t('networks.moduleOff')}</span>
                  {:else}<Switch checked={on} labelledby="mod-{m.name}-l" onchange={(v) => setMod(m.name, v)} />{/if}
                </div>
                {#if on && !m.off && m.settings?.length}
                  <div class="mod-settings">
                    {#each m.settings as st (st.key)}
                      {@const fid = `mod-${m.name}-${st.key}`}
                      {#if st.kind === 'lines'}
                        <Field id={fid} label={st.label} help={st.help} encrypted><textarea class="input mono" id={fid} rows="4" value={val(m, st)} oninput={(e) => setVal(m, st, e.currentTarget.value)}></textarea></Field>
                      {:else if st.kind === 'bool'}
                        <div class="check-row"><span class="od-field"><span id="{fid}-l">{st.label}</span>{#if st.help}<span>{st.help}</span>{/if}</span><Switch checked={['yes', 'true', 'on', '1'].includes(String(val(m, st)).toLowerCase())} labelledby="{fid}-l" onchange={(v) => setVal(m, st, v ? 'yes' : 'no')} /></div>
                      {:else}
                        <Field id={fid} label={st.label} help={st.help} encrypted><input class="input mono" id={fid} inputmode={st.kind === 'number' ? 'numeric' : 'text'} value={val(m, st)} oninput={(e) => setVal(m, st, e.currentTarget.value)} /></Field>
                      {/if}
                    {/each}
                  </div>
                {/if}
              </div>
            {:else}
              <p class="desc">{t('networks.loadingModules')}</p>
            {/each}
          </div>
        </div>
      </section>

      <section class="card" id="advanced" aria-labelledby="advanced-h">
        <div class="panel-grid">
          <div class="od-stack"><h2 id="advanced-h">{t('networks.section.advanced')}</h2><p class="desc">{t('networks.advancedIntro')}</p></div>
          <div class="form-grid">
            <Field id="encoding" label={t('networks.encoding')}>
              <Select id="encoding" bind:value={f.encodingName} options={[{ value: '', label: 'UTF-8' }, ...ENCODINGS.map((e) => ({ value: e, label: e }))]} />
            </Field>
            {#if f.encodingName}
              <Field id="encMode" label={t('networks.encMode')}>
                <Select id="encMode" value={f.encodingAlways ? 'always' : 'fallback'} onchange={(v) => (f.encodingAlways = v === 'always')} options={[
                  { value: 'fallback', label: t('networks.encFallback') },
                  { value: 'always', label: t('networks.encAlways', { encoding: f.encodingName }) },
                ]} />
              </Field>
            {:else}<span></span>{/if}
            <div class="check-row span-2"><span class="od-field"><span id="flood-l">{t('networks.flood')}</span><span>{t('networks.floodHelp')}</span></span><Switch checked={f.floodOn} labelledby="flood-l" onchange={(v) => (f.floodOn = v)} /></div>
            {#if f.floodOn}
              <Field id="floodRate" label={t('networks.floodRate')} err={errs.floodRate}><input class="input mono" id="floodRate" inputmode="decimal" bind:value={f.floodRate} aria-invalid={!!errs.floodRate} /></Field>
              <Field id="floodBurst" label={t('networks.floodBurst')} help={t('networks.floodBurstHelp')} err={errs.floodBurst}><input class="input mono" id="floodBurst" inputmode="numeric" bind:value={f.floodBurst} aria-invalid={!!errs.floodBurst} /></Field>
            {/if}
            <Field id="joinDelay" label={t('networks.joinDelay')} help={t('networks.joinDelayHelp')} err={errs.joinDelay}><input class="input mono" id="joinDelay" inputmode="numeric" bind:value={f.joinDelay} aria-invalid={!!errs.joinDelay} /></Field>
          </div>
        </div>
      </section>

      {#if net}
        <section class="card" id="log" aria-labelledby="log-h">
          <div class="section-head" style="margin-bottom:var(--s3)">
            <div class="od-stack"><h2 id="log-h">{t('networks.section.log')}</h2><p class="desc">{t('networks.logIntro')}</p></div>
            {#if log?.length}<button class="btn btn-ghost btn-sm" type="button" onclick={copyLog}><Icon name="copy" />{t('networks.copy')}</button>{/if}
          </div>
          {#if !log}
            <div class="skel" style="height:80px"></div>
          {:else if !log.length}
            <p class="desc" style="font-size:var(--fs-sm)">{t('networks.logEmpty')}</p>
          {:else}
            <ConnLog entries={log} />
          {/if}
        </section>

        <section class="card danger-card" id="danger" aria-labelledby="danger-h">
          <div class="panel-grid">
            <div class="od-stack"><h2 id="danger-h">{t('networks.removeNetwork')}</h2><p class="desc">{t('networks.removeIntro')}</p></div>
            <div><button class="btn btn-danger" type="button" onclick={remove}><Icon name="trash" />{t('networks.removeName', { name: net.name })}</button></div>
          </div>
        </section>
      {/if}

      <div class="save-bar">
        <a class="btn btn-ghost" href="/networks">{t('common.cancel')}</a>
        <button class="btn btn-primary" type="submit" disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('networks.saving')}{:else}<Icon name="check" />{id == null ? t('networks.addNetwork') : t('networks.saveChanges')}{/if}</button>
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
