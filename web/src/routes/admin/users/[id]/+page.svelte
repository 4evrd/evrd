<script>
  // One user, for admins: their overview as they see it, then their networks
  // and channels, attached clients and sign-ins, and the account controls.
  import { onMount, onDestroy } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import BouncerSettings from '$lib/components/BouncerSettings.svelte'
  import UserOverview from '$lib/components/UserOverview.svelte'
  import ConnLog from '$lib/components/ConnLog.svelte'
  import Reconnects from '$lib/components/Reconnects.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago, date, since, device, limit } from '$lib/format.js'
  import { check } from '$lib/validate.js'
  import { t } from '$lib/i18n.svelte.js'

  const id = $derived(+page.params.id)
  const isAdmin = $derived(session.me?.role === 'admin')
  let cur = $state(null) // /api/admin/users/{id}
  let ov = $state(null)  // /api/admin/users/{id}/overview
  let rc = $state(null)  // /api/admin/users/{id}/reconnects
  let err = $state('')
  let ovErr = $state('')
  let lim = $state({ maxNetworks: '', maxClients: '', backlogLines: '' })
  let limErr = $state({})
  let saving = $state(false)
  let tempPw = $state('')
  let timer

  async function loadUser(first) {
    try {
      const d = await api.admin.user(id)
      cur = d
      if (first) lim = { maxNetworks: d.overrides.maxNetworks ?? '', maxClients: d.overrides.maxClients ?? '', backlogLines: d.overrides.backlogLines ?? '' }
      err = ''
    } catch (e) { err = e.message }
  }
  async function loadOverview() {
    try { ov = await api.admin.userOverview(id); ovErr = '' } catch (e) { ovErr = e.message }
    try { rc = await api.admin.userReconnects(id) } catch { /* the overview shows the error */ }
  }
  onMount(async () => {
    if (!isAdmin) return
    await Promise.all([loadUser(true), loadOverview()])
    timer = setInterval(() => { loadUser(false); loadOverview() }, 15000)
  })
  onDestroy(() => clearInterval(timer))

  const self = $derived(cur && cur.id === session.me.id)
  const status = $derived(cur?.suspended ? 'suspended' : (ov?.clients?.length ? 'online' : 'offline'))
  // the overview's networks carry live channels; the user detail has settings
  const liveNet = (nid) => ov?.networks?.find((n) => n.id === nid)
  const nets = $derived(cur?.networks || [])
  const ircClients = $derived((ov?.clients || []).filter((c) => c.kind === 'irc'))
  const webClients = $derived((ov?.clients || []).filter((c) => c.kind === 'web'))
  // the most recent sign-ins first; the rest behind a button
  let allSessions = $state(false)
  const sessions = $derived([...(ov?.sessions || [])].sort((a, b) => b.lastSeenAt - a.lastSeenAt))
  const shownSessions = $derived(allSessions ? sessions : sessions.slice(0, 5))

  // connection logs, opened per network (refreshed with the page)
  let logs = $state({})
  async function loadLog(nid) {
    try { logs[nid] = await api.admin.networkLog(id, nid) } catch (e) { logs[nid] = [{ at: Date.now(), from: 'evrd', text: t('adminUsers.logFailed', { error: e.message }), bad: true }] }
  }
  function toggleLog(nid) { if (logs[nid]) delete logs[nid]; else loadLog(nid) }
  $effect(() => { if (cur) for (const k of Object.keys(logs)) loadLog(+k) })

  async function patch(body, msg) {
    try {
      const d = await api.admin.updateUser(cur.id, body)
      cur = { ...cur, ...d, networks: cur.networks }
      if (self) await refreshMe()
      toast(msg)
      return true
    } catch (e) { toast(e.message.charAt(0).toUpperCase() + e.message.slice(1), 'bad'); return false }
  }
  async function setAdmin(on) {
    if (!on && self && !(await confirmDialog({ title: t('adminUsers.removeOwnAdminTitle'), body: t('adminUsers.removeOwnAdminBody'), action: t('adminUsers.removeOwnAdminAction') }))) return
    if (await patch({ role: on ? 'admin' : 'user' }, t(on ? 'adminUsers.nowAdmin' : 'adminUsers.noLongerAdmin', { name: cur.username })) && !on && self) location.href = '/'
  }
  async function saveLimits(e) {
    e.preventDefault()
    limErr = { maxNetworks: check('count', String(lim.maxNetworks)), maxClients: check('count', String(lim.maxClients)), backlogLines: check('count', String(lim.backlogLines)) }
    if (Object.values(limErr).some(Boolean)) return
    const num = (v) => (String(v).trim() === '' ? null : +v)
    saving = true
    if (await patch({ limits: { maxNetworks: num(lim.maxNetworks), maxClients: num(lim.maxClients), backlogLines: num(lim.backlogLines) } }, t('adminUsers.savedLimits', { name: cur.username }))) loadOverview()
    saving = false
  }
  async function saveBouncer(settings, denySetBindHost) {
    const d = await api.admin.updateUser(cur.id, { settings, denySetBindHost })
    cur = { ...cur, ...d, networks: cur.networks }
    return d.settings
  }
  async function resetPassword() {
    if (!(await confirmDialog({ title: t('adminUsers.resetPasswordTitle', { name: cur.username }), body: t('adminUsers.resetPasswordBody'), action: t('adminUsers.resetPasswordAction'), danger: false }))) return
    try { const r = await api.admin.resetPassword(cur.id); tempPw = r.password; toast(t('adminUsers.tempPasswordMade', { name: cur.username })); loadOverview() } catch (e) { toast(e.message, 'bad') }
  }
  async function signOutEverywhere() {
    if (!(await confirmDialog({ title: t('adminUsers.signOutTitle', { name: cur.username }), body: t('adminUsers.signOutBody'), action: t('adminUsers.signOutEverywhere'), danger: false }))) return
    try { await api.admin.revokeSessions(cur.id); toast(t('adminUsers.signedOut', { name: cur.username })); loadOverview() } catch (e) { toast(e.message, 'bad') }
  }
  async function toggleSuspend() {
    if (cur.suspended) { await patch({ suspended: false }, t('adminUsers.restored', { name: cur.username })); loadOverview(); return }
    if (!(await confirmDialog({ title: t('adminUsers.suspendTitle', { name: cur.username }), body: t('adminUsers.suspendBody'), action: t('adminUsers.suspend') }))) return
    await patch({ suspended: true }, t('adminUsers.suspended', { name: cur.username }))
    loadOverview()
  }
  async function del() {
    if (self) { toast(t('adminUsers.cantDeleteSelf'), 'bad'); return }
    if (!(await confirmDialog({ title: t('adminUsers.deleteTitle', { name: cur.username }), body: t('adminUsers.deleteBody'), action: t('adminUsers.deleteUser'), typed: cur.username }))) return
    try { await api.admin.deleteUser(cur.id); toast(t('adminUsers.deleted', { name: cur.username })); goto('/admin/users') } catch (e) { toast(e.message, 'bad') }
  }
</script>

<svelte:head><title>{t('adminUsers.userPageTitle', { name: cur?.username || t('adminUsers.roleUser') })}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('adminUsers.noAccess')}</span></div>
{:else if err && !cur}
  <a class="back" href="/admin/users"><Icon name="chevron-left" class="i-sm" />{t('adminUsers.heading')}</a>
  <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('adminUsers.loadUserFailed', { error: err })}</span></div>
{:else if !cur}
  <div class="skel" style="height:80px"></div><div class="skel" style="margin-top:12px;height:160px"></div>
{:else}
  <a class="back" href="/admin/users"><Icon name="chevron-left" class="i-sm" />{t('adminUsers.heading')}</a>
  <div class="page-head enter">
    <div class="user-hero">
      <span class="avatar" aria-hidden="true">{cur.username[0].toUpperCase()}</span>
      <div class="titles">
        <h1 data-testid="user-title">{cur.username}</h1>
        <p class="od-row" style="flex-wrap:wrap;gap:var(--s2)">
          {#if cur.role === 'admin'}<span class="chip ok"><Icon name="crown" />{t('adminUsers.roleAdmin')}</span>{:else}<span class="chip">{t('adminUsers.roleUser')}</span>{/if}
          {#if status === 'online'}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('adminUsers.online')}</span>{:else if status === 'suspended'}<span class="chip bad"><Icon name="ban" />{t('admin.suspended')}</span>{:else}<span class="chip"><span class="dot" aria-hidden="true"></span>{t('adminUsers.offline')}</span>{/if}
          <span class="faint">{cur.sso ? t('adminUsers.signsInWith', { provider: session.config.ssoLabel }) : t('adminUsers.passwordSignIn')}{cur.email ? ' · ' + cur.email : ''} · {t('adminUsers.joined', { date: date(cur.createdAt) })} · {status === 'online' ? t('adminUsers.hereNow') : t('adminUsers.lastSeen', { time: ago(cur.lastSeenAt || cur.lastLoginAt) })}</span>
        </p>
      </div>
    </div>
    {#if !self}
      <button class={cur.suspended ? 'btn' : 'btn btn-danger-ghost'} type="button" onclick={toggleSuspend}><Icon name={cur.suspended ? 'refresh' : 'ban'} />{cur.suspended ? t('adminUsers.restore') : t('adminUsers.suspend')}</button>
    {/if}
  </div>

  {#if ovErr && !ov}<div class="callout bad" role="alert"><Icon name="alert" /><span>{t('adminUsers.liveFailed', { name: cur.username, error: ovErr })}</span></div>{/if}
  <UserOverview {ov} usage={ov?.usage || { networks: nets.length }} limits={ov?.limits || cur.limits || {}} admin who={cur.username} />

  <section class="card enter" id="reconnects" aria-labelledby="rc-h">
    <div class="section-head"><div class="od-stack"><span class="caps">{t('adminUsers.reconnects')}</span><h2 id="rc-h">{t('adminUsers.linkTrouble')}</h2></div></div>
    <Reconnects data={rc} logHref={(n) => `#net-${n.id}`} />
  </section>

  <section class="card enter" id="networks" aria-labelledby="nets-h">
    <div class="section-head">
      <div class="od-stack"><span class="caps">{t('adminUsers.colNetworks')}</span><h2 id="nets-h">{t('adminUsers.networksAndChannels')}</h2></div>
      <span class="chip">{t('adminUsers.nOf', { n: nets.length, max: limit(cur.limits?.maxNetworks ?? 0) })}</span>
    </div>
    <div class="net-list">
      {#each nets as n (n.id)}
        {@const live = liveNet(n.id)}
        <article class="net-card" id="net-{n.id}">
          <div class="net-top">
            <span class="od-field od-fill"><span class="name">{n.name}</span><span class="sub mono od-truncate">{n.servers?.length > 1 ? t('adminUsers.nServers', { count: n.servers.length }) + ' · ' : ''}{n.host}:{n.port}{n.tls ? ' · TLS' : ''}</span></span>
            {#if !n.enabled}<span class="chip">{t('adminUsers.switchedOff')}</span>{/if}
            <StatusChip status={n.status} />
          </div>
          <div class="net-facts">
            <div><span class="caps">{t('adminUsers.nick')}</span><span class="mono">{n.currentNick || n.nick || '—'}</span></div>
            <div><span class="caps">{t('adminUsers.lag')}</span><span class="mono">{n.lag != null ? n.lag + ' ms' : '—'}</span></div>
            <div><span class="caps">{t('adminUsers.upFor')}</span><span class="mono">{n.status === 'connected' ? since(n.connectedAt) : '—'}</span></div>
            <div><span class="caps">{t('adminUsers.unread')}</span><span class="mono">{n.unread}{n.mentions ? ' · ' + t('adminUsers.nMentions', { count: n.mentions }) : ''}</span></div>
          </div>
          {#if n.lastError && n.status !== 'connected'}<p class="callout warn" data-testid="net-problem"><Icon name="alert" /><span><b>{n.lastError}.</b>{#if n.hint} {n.hint}{/if}</span></p>{/if}
          <div><button class="btn btn-ghost btn-sm" type="button" aria-expanded={!!logs[n.id]} onclick={() => toggleLog(n.id)}><Icon name={logs[n.id] ? 'chevronDown' : 'chevron'} class="i-sm" />{t('adminUsers.connectionLog')}</button></div>
          {#if logs[n.id]}
            {#if logs[n.id].length}<ConnLog entries={logs[n.id]} />{:else}<p class="desc" style="font-size:var(--fs-xs)">{t('adminUsers.nothingLogged')}</p>{/if}
          {/if}
          <div class="chan-chips">
            {#each live?.buffers || [] as b}
              <span class="chan-chip"><Icon name="hash" class="i-sm" /><span>{b.name.replace(/^#/, '')}</span>{#if b.members != null}<span class="faint">{b.members}</span>{/if}{#if b.mentions}<span class="count mention">{b.mentions}</span>{:else if b.unread}<span class="count">{b.unread}</span>{/if}</span>
            {:else}
              <span class="faint" style="font-size:var(--fs-xs)">{n.status === 'connected' ? t('adminUsers.inNoChannels') : n.channels.length ? t('adminUsers.savedChannels', { channels: n.channels.join(', ') }) : t('adminUsers.noChannelsSaved')}</span>
            {/each}
          </div>
        </article>
      {:else}
        <p class="desc">{t('adminUsers.noNetworks', { name: cur.username })}</p>
      {/each}
    </div>
  </section>

  <section class="card enter" id="clients" aria-labelledby="cli-h">
    <div class="section-head">
      <div class="od-stack"><span class="caps">{t('adminUsers.colClients')}</span><h2 id="cli-h">{t('adminUsers.clientsAndSignIns')}</h2></div>
      <span class="chip">{t('adminUsers.ircClientsOf', { n: ircClients.length, max: limit(cur.limits?.maxClients ?? 0) })}</span>
    </div>
    <div class="cli-grid">
      <div class="od-stack" style="--od-gap:var(--s2)">
        <span class="caps">{t('adminUsers.attachedNow')}</span>
        {#each [...ircClients, ...webClients] as c (c.id)}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="od-field"><span class="name">{c.kind === 'web' ? t('adminUsers.webClient', { software: c.software || t('adminUsers.browser') }) : c.software || t('adminUsers.ircClient')}</span><span class="sub mono od-truncate">{c.networks.join(', ') || t('adminUsers.noNetwork')}{c.ip ? ' · ' + c.ip : ''}{c.device ? ' · ' + c.device : ''}</span></span>
            <span class="chip ok">{since(c.since)}</span>
          </div>
        {:else}<p class="desc" style="font-size:var(--fs-sm)">{t('adminUsers.nothingAttached')}</p>{/each}
      </div>
      <div class="od-stack" style="--od-gap:var(--s2)">
        <div class="od-row" style="justify-content:space-between"><span class="caps">{t('adminUsers.webSignIns')}</span>{#if ov?.sessions?.length}<button class="btn btn-ghost btn-sm" type="button" onclick={signOutEverywhere}>{t('adminUsers.signOutEverywhere')}</button>{/if}</div>
        {#each shownSessions as s (s.id)}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="od-field"><span class="name">{device(s.userAgent)}</span><span class="sub mono od-truncate">{s.ip || t('adminUsers.unknownAddress')} · {s.method === 'oidc' ? session.config.ssoLabel : t('adminUsers.methodPassword')}</span></span>
            <span class="faint" style="font-size:var(--fs-xs)">{ago(s.lastSeenAt)}</span>
          </div>
        {:else}<p class="desc" style="font-size:var(--fs-sm)">{t('adminUsers.notSignedIn')}</p>{/each}
        {#if sessions.length > 5}<button class="btn btn-ghost btn-sm" type="button" style="justify-self:start" onclick={() => (allSessions = !allSessions)}>{allSessions ? t('adminUsers.showFewer') : t('adminUsers.showAll', { count: sessions.length })}</button>{/if}
        <span class="caps" style="margin-top:var(--s3)">{t('adminUsers.clientPasswords')}</span>
        {#each ov?.clientPasswords || [] as p}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="od-field"><span class="name">{p.name}</span><span class="sub">{t('adminUsers.made', { date: date(p.createdAt) })}</span></span>
            <span class="faint" style="font-size:var(--fs-xs)">{p.lastUsedAt ? t('adminUsers.used', { time: ago(p.lastUsedAt) }) : t('adminUsers.neverUsed')}</span>
          </div>
        {:else}<p class="desc" style="font-size:var(--fs-sm)">{t('adminUsers.noClientPasswords')}</p>{/each}
      </div>
    </div>
  </section>

  <section class="card enter" id="manage" aria-labelledby="man-h">
    <div class="section-head"><div class="od-stack"><span class="caps">{t('adminUsers.account')}</span><h2 id="man-h">{t('adminUsers.limitsAndAccess')}</h2></div></div>
    <div class="manage-grid">
      <form class="form od-stack" style="--od-gap:12px" novalidate onsubmit={saveLimits}>
        <span class="caps">{t('adminUsers.limits')}</span>
        <div class="limit-grid">
          <Field id="l-net" label={t('adminUsers.maxNetworks')} err={limErr.maxNetworks}><input class="input mono" id="l-net" inputmode="numeric" placeholder={t('adminUsers.default')} bind:value={lim.maxNetworks} /></Field>
          <Field id="l-cli" label={t('adminUsers.maxClients')} err={limErr.maxClients}><input class="input mono" id="l-cli" inputmode="numeric" placeholder={t('adminUsers.default')} bind:value={lim.maxClients} /></Field>
          <Field id="l-back" label={t('adminUsers.backlogLines')} err={limErr.backlogLines}><input class="input mono" id="l-back" inputmode="numeric" placeholder={t('adminUsers.default')} bind:value={lim.backlogLines} /></Field>
        </div>
        <span class="help">{t('adminUsers.limitsHelp')}</span>
        <div><button class="btn btn-primary btn-sm" type="submit" disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('adminUsers.saving')}{:else}<Icon name="check" />{t('adminUsers.saveLimits')}{/if}</button></div>
      </form>
      <div class="od-stack" style="--od-gap:12px">
        <span class="caps">{t('adminUsers.access')}</span>
        <div class="action-list">
          <div class="row"><span class="od-field"><span class="name" id="adm-l">{t('adminUsers.administrator')}</span><span class="sub">{t('adminUsers.administratorHelp')}</span></span><Switch checked={cur.role === 'admin'} labelledby="adm-l" onchange={setAdmin} /></div>
          <div class="row"><span class="od-field"><span class="name">{t('adminUsers.password')}</span><span class="sub">{t('adminUsers.passwordHelp')}</span></span><button class="btn btn-sm" type="button" onclick={resetPassword}><Icon name="key" />{t('adminUsers.reset')}</button></div>
          {#if tempPw}
            <div class="reveal-box">
              <div class="od-row"><Icon name="key" /><strong>{t('adminUsers.tempPasswordFor', { name: cur.username })}</strong></div>
              <p class="desc" style="color:var(--fg)">{t('adminUsers.shareIt')}</p>
              <div class="od-row"><input class="input mono od-fill" readonly value={tempPw} aria-label={t('adminUsers.tempPassword')} /><Copy text={tempPw} label={t('adminUsers.copyPassword')} size={44} /></div>
            </div>
          {/if}
          {#if !self}
            <div class="row"><span class="od-field"><span class="name">{t('adminUsers.deleteAccount')}</span><span class="sub">{t('adminUsers.deleteAccountHelp')}</span></span><button class="btn btn-danger-ghost btn-sm" type="button" onclick={del}><Icon name="trash" />{t('adminUsers.delete')}</button></div>
          {/if}
        </div>
      </div>
    </div>
    {#if cur.settings}
      <details class="bouncer-admin">
        <summary class="caps">{t('adminUsers.bouncerSettings')}</summary>
        {#key cur.id}<BouncerSettings value={cur.settings} save={saveBouncer} admin compact />{/key}
      </details>
    {/if}
  </section>
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: var(--fs-sm); margin-bottom: var(--s3); text-decoration: none; }
  .back:hover { color: var(--fg); }
  .user-hero { display: flex; align-items: center; gap: var(--s4); min-width: 0; }
  .user-hero .avatar { width: 56px; height: 56px; font-size: var(--fs-xl); flex: none; }
  section.card { margin-top: var(--s6); scroll-margin-top: 80px; }
  .net-list { display: grid; gap: var(--s3); }
  .net-card { display: grid; gap: var(--s3); padding: var(--s4); border-radius: var(--r-card); background: var(--bg-deep); border: 1px solid var(--line-soft); scroll-margin-top: 80px; }
  .net-card:target { border-color: var(--cyan); }
  .net-top { display: flex; align-items: center; gap: var(--s3); min-width: 0; }
  .net-top .name { font-weight: 600; }
  .net-facts { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--s3); }
  .net-facts > div { display: grid; gap: 2px; min-width: 0; }
  .net-facts .mono { font-family: var(--font-mono); font-size: var(--fs-sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chan-chips { display: flex; flex-wrap: wrap; gap: var(--s2); }
  .chan-chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 999px; background: var(--glass); border: 1px solid var(--line-soft); font-family: var(--font-mono); font-size: var(--fs-xs); }
  .cli-grid, .manage-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--s6); }
  .action-list { display: grid; gap: var(--s2); }
  .action-list .row { --cols: minmax(0, 1fr) auto; }
  .limit-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--s3); }
  .bouncer-admin { margin-top: var(--s5); }
  .bouncer-admin summary { cursor: pointer; padding: var(--s2) 0; }
  @media (max-width: 899px) { .cli-grid, .manage-grid { grid-template-columns: minmax(0, 1fr); } .net-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 639px) { .limit-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
