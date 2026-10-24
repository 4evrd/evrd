<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import Drawer from '$lib/components/Drawer.svelte'
  import Field from '$lib/components/Field.svelte'
  import Msg from '$lib/components/Msg.svelte'
  import { api } from '$lib/api.js'
  import { session, ircPort } from '$lib/session.svelte.js'
  import { toast, confirmDialog, openDrawer, closeDrawers } from '$lib/ui.svelte.js'
  import { date, ago, since, limit } from '$lib/format.js'
  import { check } from '$lib/validate.js'
  import { t } from '$lib/i18n.svelte.js'

  const me = $derived(session.me)
  const host = $derived(session.config.ircHost)
  const ports = $derived(session.config.ircPorts || [])
  const port = $derived(ircPort())
  const firstNet = $derived(session.nets[0]?.name || 'libera')

  let pws = $state(null)
  let attached = $state(null)
  let timer
  async function load() {
    try { pws = await api.clientPasswords() } catch (e) { if (!pws) toast(e.message, 'bad') }
    try { attached = await api.clients() } catch { /* keep */ }
  }
  onMount(() => { load(); timer = setInterval(async () => { try { attached = await api.clients() } catch { /* keep */ } }, 10000) })
  onDestroy(() => clearInterval(timer))

  let tab = $state('hex')
  // How the guide signs in: by link (no password; *status sends a link to
  // allow on this website) when the server offers it, else a password. SSO
  // accounts start on the link, password accounts on their password.
  const linkOn = $derived(!!session.config.ircLinkSignIn)
  let method = $state(null)
  const byLink = $derived(linkOn && (method ?? (me.hasPassword ? 'password' : 'link')) === 'link')
  const TABS = $derived([['hex', 'HexChat'], ['mirc', 'mIRC'], ['irssi', 'irssi'], ['wee', 'WeeChat'], ['other', t('clients.other')]])

  // new password drawer
  let name = $state('')
  let nameErr = $state('')
  let created = $state(null)
  let busy = $state(false)
  function openNew() { name = ''; nameErr = ''; created = null; openDrawer('pw-drawer') }
  async function create(e) {
    e.preventDefault()
    if (created) return
    nameErr = check('name', name)
    if (nameErr) { document.getElementById('pw-name')?.focus(); return }
    busy = true
    try {
      const cp = await api.createClientPassword(name.trim())
      created = cp
      await load()
      toast(t('clients.created'))
      setTimeout(() => document.getElementById('pw-value')?.select(), 50)
    } catch (err) { nameErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' } finally { busy = false }
  }
  async function revoke(p) {
    const ok = await confirmDialog({ title: t('clients.revokeTitle', { name: p.name }), body: t('clients.revokeBody'), action: t('clients.revoke') })
    if (!ok) return
    try { await api.deleteClientPassword(p.id); await load(); toast(t('clients.revoked', { name: p.name })) } catch (e) { toast(e.message, 'bad') }
  }
  const pwText = $derived(created?.password || 'PASSWORD')
  const ircCount = $derived((attached?.clients || []).filter((c) => c.kind === 'irc').length)
</script>

<svelte:head><title>{t('clients.title')} · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>{t('clients.title')}</h1>
    <p>{t('clients.intro')}</p>
  </div>
  <button class="btn btn-primary" type="button" onclick={openNew}><Icon name="key" />{t('clients.newPassword')}</button>
</div>

{#if !me.hasPassword && session.config.ssoIRCPassword}
  <div class="callout enter" role="note" data-testid="sso-works" style="margin-bottom:var(--s6)">
    <Icon name="key" />
    <span class="od-fill"><b>{t('clients.ssoWorksTitle', { sso: session.config.ssoLabel || 'SSO' })}</b> <Msg key="clients.ssoWorks" vars={{}}>{#snippet format()}<span class="code">{me.username}/{firstNet}:<i>{t('clients.passwordWord')}</i></span>{/snippet}</Msg></span>
  </div>
{:else if !me.hasPassword && linkOn}
  <div class="callout enter" role="note" data-testid="link-notice" style="margin-bottom:var(--s6)">
    <Icon name="key" />
    <span class="od-fill"><b>{t('clients.linkTitle')}</b> <Msg key="clients.linkBody" vars={{}}>{#snippet format()}<span class="code">{me.username}/{firstNet}</span>{/snippet}</Msg></span>
  </div>
{:else if !me.hasPassword}
  <div class="callout warn enter" role="note" data-testid="sso-notice" style="margin-bottom:var(--s6)">
    <Icon name="alert" />
    <span class="od-fill"><b>{t('clients.ssoNoticeTitle', { sso: session.config.ssoLabel || 'SSO' })}</b> <Msg key="clients.ssoNotice" vars={{ sso: session.config.ssoLabel || t('clients.singleSignOn') }}>{#snippet format()}<span class="code">{me.username}/{firstNet}:<i>{t('clients.clientPasswordWord')}</i></span>{/snippet}</Msg></span>
    {#if pws && !pws.length}<button class="btn btn-primary btn-sm" type="button" onclick={openNew}><Icon name="key" />{t('clients.createOne')}</button>{/if}
  </div>
{/if}

<div class="grid-main-side">
  <div class="side-stack" style="display:grid;gap:var(--s6)">
    <section class="card enter" aria-labelledby="conn-h">
      <div class="section-head">
        <div class="od-stack"><span class="caps">{t('clients.connection')}</span><h2 id="conn-h">{t('clients.pointHere')}</h2></div>
        {#if ports.some((p) => p.tls)}<span class="chip ok"><Icon name="shield" />{t('clients.tlsAvailable')}</span>{/if}
      </div>
      <div class="kv-list">
        <div class="kv"><div class="od-field"><span class="k">{t('clients.server')}</span><span class="v" data-testid="irc-host">{host}</span></div><Copy text={host} label={t('clients.copyServer')} /></div>
        <div class="kv"><div class="od-field"><span class="k">{t('clients.port')}</span><span class="v">{ports.map((p) => `${p.port} (${p.tls ? 'TLS' : t('clients.plain')})`).join(' · ') || port.port}</span></div><Copy text={String(port.port)} label={t('clients.copyPort')} /></div>
        <div class="kv"><div class="od-field"><span class="k">{t('clients.serverPassword')}</span><span class="v" data-testid="pass-format">{me.username}/<i>{t('clients.networkWord')}</i>{#if !byLink}:<i>{t('clients.passwordWord')}</i>{/if}</span></div><Copy text={byLink ? `${me.username}/${firstNet}` : `${me.username}/${firstNet}:`} label={t('clients.copyFormat')} /></div>
      </div>
      <p class="desc" style="margin-top:var(--s4)">{me.hasPassword ? t('clients.useAccountPassword') : session.config.ssoIRCPassword ? t('clients.useSsoOrClientPassword', { sso: session.config.ssoLabel || 'SSO' }) : t('clients.useClientPassword', { sso: session.config.ssoLabel })} <Msg key={session.nets[0] ? 'clients.leaveOutNamed' : 'clients.leaveOut'} vars={{ network: session.nets[0]?.name }}>{#snippet path()}<span class="code">/network</span>{/snippet}</Msg>{#if linkOn && !byLink} {t('clients.linkHint')}{/if}</p>
    </section>

    <section class="card enter" aria-labelledby="guide-h">
      <div class="section-head"><div class="od-stack"><span class="caps">{t('clients.setupGuide')}</span><h2 id="guide-h">{t('clients.setUp')}</h2></div></div>
      <div style="display:grid;grid-template-columns:minmax(0, 1fr);gap:var(--s4)">
        {#if linkOn}
          <div class="tabs" role="tablist" aria-label={t('clients.methodLabel')} data-testid="signin-method">
            <button class="tab" role="tab" aria-selected={byLink} onclick={() => (method = 'link')}>{t('clients.methodLink')}</button>
            <button class="tab" role="tab" aria-selected={!byLink} onclick={() => (method = 'password')}>{t('clients.methodPassword')}</button>
          </div>
          {#if byLink}<p class="desc" data-testid="link-steps">{t('clients.linkSteps')}</p>{/if}
        {/if}
        <div class="tabs" role="tablist" aria-label={t('clients.client')}>
          {#each TABS as [k, l]}<button class="tab" role="tab" aria-selected={tab === k} onclick={() => (tab = k)}>{l}</button>{/each}
        </div>
        {#if tab === 'hex'}
          <div class="od-stack" style="--od-gap:12px">
            <p class="desc">{t('clients.hexchatSteps')}</p>
<pre class="snippet">Servers         <b>{host}/{port.port}</b>
Use SSL         <b>{port.tls ? 'on' : 'off'}</b>
Login method    <em>Server password (/PASS password)</em>
Password        <b>{me.username}/{firstNet}{byLink ? '' : ':'}</b>{#if !byLink}<em>{pwText}</em>{/if}</pre>
          </div>
        {:else if tab === 'mirc'}
          <div class="od-stack" style="--od-gap:12px">
            <p class="desc">{t('clients.mircSteps')}</p>
<pre class="snippet">Description     <b>evrd-{firstNet}</b>
IRC Server      <b>{host}</b>
Ports           <b>{port.tls ? '+' : ''}{port.port}</b>
Password        <b>{me.username}/{firstNet}{byLink ? '' : ':'}</b>{#if !byLink}<em>{pwText}</em>{/if}</pre>
            <p class="desc">{port.tls ? t('clients.mircCommandTls') : t('clients.mircCommand')}</p>
<pre class="snippet">/server -m <b>{host}</b> {port.tls ? '+' : ''}{port.port} <b>{me.username}/{firstNet}{byLink ? '' : ':'}</b>{#if !byLink}<em>{pwText}</em>{/if}</pre>
          </div>
        {:else if tab === 'irssi'}
<pre class="snippet">/network add <b>evrd-{firstNet}</b>
/server add {port.tls ? '-tls ' : ''}-network <b>evrd-{firstNet}</b> -password <b>{me.username}/{firstNet}{byLink ? '' : ':'}</b>{#if !byLink}<em>{pwText}</em>{/if} <b>{host}</b> {port.port}
/connect <b>evrd-{firstNet}</b></pre>
        {:else if tab === 'wee'}
<pre class="snippet">/server add <b>evrd</b> <b>{host}</b>/{port.port}{port.tls ? ' -tls' : ''}
/set irc.server.evrd.password "<b>{me.username}/{firstNet}{byLink ? '' : ':'}</b>{#if !byLink}<em>{pwText}</em>{/if}"
/connect <b>evrd</b></pre>
        {:else}
          <p class="desc">{t('clients.otherWorks')} <Msg key={port.tls ? 'clients.otherUseTls' : 'clients.otherUse'}>{#snippet hostname()}<span class="code">{host}</span>{/snippet}{#snippet portnum()}<span class="code">{port.port}</span>{/snippet}</Msg> {t('clients.otherPerNetwork')}</p>
        {/if}
        {#if port.tls && session.config?.ircSelfSigned}<p class="desc" style="font-size:var(--fs-xs)">{t('clients.selfSigned')}</p>{/if}
      </div>
    </section>
  </div>

  <div class="side-stack" style="display:grid;gap:var(--s6)">
    <section class="card enter" aria-labelledby="pw-h">
      <div class="section-head" style="margin-bottom:var(--s3)"><div class="od-stack"><span class="caps">{t('clients.clientPasswords')}</span><h2 id="pw-h">{t('clients.perDevice')}</h2></div></div>
      <p class="desc" style="margin-bottom:var(--s4)">{t('clients.perDeviceHelp')}</p>
      <div class="rows">
        {#if !pws}
          <div class="skel"></div>
        {:else}
          {#each pws as p (p.id)}
            <div class="row pw-row">
              <span class="pic"><Icon name="key" /></span>
              <span class="od-field"><span class="name od-truncate">{p.name}</span><span class="sub">{t('clients.createdUsed', { created: date(p.createdAt), used: ago(p.lastUsedAt) })}{#if p.remembered} · <span data-testid="remembered">{p.expiresAt * 1000 > Date.now() ? t('clients.rememberedUntil', { date: date(p.expiresAt) }) : t('clients.rememberedExpired')}</span>{/if}</span></span>
              <span></span>
              <button class="btn btn-danger-ghost btn-sm" type="button" aria-label={t('clients.revokeName', { name: p.name })} onclick={() => revoke(p)}>{t('clients.revoke')}</button>
            </div>
          {:else}
            <div class="empty" style="padding:24px 8px"><span class="ic"><Icon name="key" /></span><h3>{t('clients.noPasswords')}</h3><p>{me.hasPassword ? t('clients.noPasswordsHelp') : t('clients.noPasswordsSso')}</p><button class="btn btn-primary btn-sm" type="button" onclick={openNew}><Icon name="plus" />{t('clients.newPassword')}</button></div>
          {/each}
        {/if}
      </div>
    </section>

    <section class="card tight enter" aria-labelledby="live-h">
      <div class="section-head" style="margin-bottom:var(--s3)">
        <h2 id="live-h" style="font-size:var(--fs-md)">{t('clients.attachedNow')}</h2>
        <span class="chip nick">{t('clients.countOf', { count: ircCount, max: limit(me.limits.maxClients) })}</span>
      </div>
      <div class="rows">
        {#each attached?.clients || [] as c (c.id)}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="od-field"><span class="name">{c.kind === 'web' ? t('clients.web') : c.software || t('clients.ircClient')}{c.device ? ' · ' + c.device : c.kind === 'web' ? ' · ' + c.software : ''}</span><span class="sub mono od-truncate">{c.networks.join(', ')}{c.ip ? ' · ' + c.ip : ''}</span></span>
            <span class="chip ok live"><span class="dot" aria-hidden="true"></span>{since(c.since)}</span>
          </div>
        {/each}
        {#if !(attached?.clients || []).some((c) => c.kind === 'web')}
          <!-- the server only sees open chat tabs; this browser is here too -->
          <div class="row" style="--cols:minmax(0,1fr) auto" data-testid="this-browser">
            <span class="od-field"><span class="name">{t('clients.thisBrowser')}</span><span class="sub od-truncate">{t('clients.thisBrowserHelp')}</span></span>
            <span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('clients.now')}</span>
          </div>
        {/if}
        {#if ircCount === 0}
          <p class="desc" style="font-size:var(--fs-sm)">{t('clients.noneAttached')}</p>
        {/if}
      </div>
    </section>
  </div>
</div>

<Drawer id="pw-drawer" title={t('clients.newPassword')} sub={t('clients.drawerSub')}>
  <form class="drawer-body form" id="pw-form" novalidate onsubmit={create}>
    <Field id="pw-name" label={t('clients.deviceName')} required help={t('clients.deviceNameHelp')} err={nameErr}>
      <input class="input" id="pw-name" placeholder={t('clients.deviceNamePlaceholder')} autocomplete="off" maxlength="64" bind:value={name} readonly={!!created} aria-invalid={!!nameErr} />
    </Field>
    {#if created}
      <div class="reveal-box" data-testid="new-secret">
        <div class="od-row"><Icon name="key" /><strong>{t('clients.copyNow')}</strong></div>
        <p class="desc" style="color:var(--fg)">{t('clients.copyNowHelp')}</p>
        <div class="od-row">
          <label class="sr-only" for="pw-value">{t('clients.newPasswordLabel')}</label>
          <input class="input mono od-fill" id="pw-value" readonly value={created.password} />
          <Copy text={created.password} label={t('clients.copyPassword')} size={44} />
        </div>
      </div>
    {/if}
  </form>
  {#snippet foot()}
    <button class="btn btn-ghost" type="button" onclick={closeDrawers}>{created ? t('clients.done') : t('clients.cancel')}</button>
    {#if !created}<button class="btn btn-primary" type="submit" form="pw-form" disabled={busy}>{#if busy}<span class="spinner" aria-hidden="true"></span>{t('clients.creating')}{:else}<Icon name="plus" />{t('clients.createPassword')}{/if}</button>{/if}
  {/snippet}
</Drawer>

<style>
  /* phones: every client's tab in view, and the steps wrap instead of
     scrolling sideways (copying gets the same text) */
  @media (max-width: 559px) {
    .tabs .tab { flex: 1 1 auto; padding-inline: 8px; }
    pre.snippet { white-space: pre-wrap; overflow-wrap: anywhere; }
  }
  .pw-row { --cols: 36px minmax(0, 1fr) auto auto; }
  .pic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--nick); }
</style>
