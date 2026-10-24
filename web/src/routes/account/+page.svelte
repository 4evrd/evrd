<script>
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import Select from '$lib/components/Select.svelte'
  import ChangePassword from '$lib/components/ChangePassword.svelte'
  import SecondFactor from '$lib/components/SecondFactor.svelte'
  import BouncerSettings from '$lib/components/BouncerSettings.svelte'
  import Msg from '$lib/components/Msg.svelte'
  import PackUpload from '$lib/components/PackUpload.svelte'
  import PackDownload from '$lib/components/PackDownload.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago, device } from '$lib/format.js'
  import { check } from '$lib/validate.js'
  import { coverage } from '$lib/langpack.js'
  import { t, languages, pick as pickLocale, setLocale, setPacks, personalPack, builtinAll, ircBuiltin, serverPack, i18n } from '$lib/i18n.svelte.js'

  const me = $derived(session.me)
  const TABS = [['profile', 'account.tabProfile'], ['bouncer', 'account.tabBouncer'], ['security', 'account.tabSecurity'], ['sessions', 'account.tabSessions'], ['danger', 'account.tabDelete']] // [id, message key]
  let tab = $state(TABS.some(([k]) => '#' + k === location.hash) ? location.hash.slice(1) : 'profile')
  function pick(k) { tab = k; history.replaceState(null, '', '#' + k) }
  function tabKey(e, i) {
    if (e.key === 'ArrowRight') { pick(TABS[(i + 1) % TABS.length][0]); document.getElementById('t-' + TABS[(i + 1) % TABS.length][0])?.focus() }
    if (e.key === 'ArrowLeft') { pick(TABS[(i - 1 + TABS.length) % TABS.length][0]); document.getElementById('t-' + TABS[(i - 1 + TABS.length) % TABS.length][0])?.focus() }
  }

  // profile
  let email = $state(session.me.email)
  let nick = $state(session.me.defaultNick)
  let alt = $state(session.me.defaultAltNick)
  let real = $state(session.me.defaultRealname)
  let away = $state(session.me.autoAway)
  let lang = $state(session.me.language || '')
  const langOptions = $derived([{ value: '', label: t('account.languageBrowser', { language: languages().find((l) => l.code === pickLocale(''))?.name }) }, ...languages().map((l) => ({ value: l.code, label: l.name }))])
  let perr = $state({})
  let saving = $state(false)
  async function saveProfile(e) {
    e.preventDefault()
    perr = { email: me.sso ? '' : check('email', email), nick: check('nick', nick), alt: check('optnick', alt) }
    if (Object.values(perr).some(Boolean)) return
    saving = true
    try {
      const patch = { defaultNick: nick.trim(), defaultAltNick: alt.trim(), defaultRealname: real.trim(), autoAway: away, language: lang }
      if (!me.sso) patch.email = email.trim()
      session.me = await api.updateMe(patch)
      await setLocale(pickLocale(session.me.language))
      toast(t('account.profileSaved'))
    } catch (err) { toast(err.message, 'bad') } finally { saving = false }
  }

  // the user's own translation, on top of the server's
  const own = $derived(i18n.personal)
  async function ownChanged(pack) {
    session.me = { ...session.me, languagePack: pack }
    setPacks({ personal: pack })
    await setLocale(pickLocale(session.me.language))
  }
  async function saveOwn(pack) {
    await ownChanged(await api.putMyLanguagePack(pack))
    toast(t('account.ownSaved', { name: pack.name }))
  }
  async function removeOwn() {
    if (!(await confirmDialog({ title: t('account.ownRemoveTitle'), body: t('account.ownRemoveBody'), action: t('account.ownRemove') }))) return
    try { await api.deleteMyLanguagePack(); await ownChanged(null); toast(t('account.ownRemoved')) } catch (e) { toast(e.message, 'bad') }
  }
  async function loadOwn() {
    return { code: own.code, name: own.name, messages: await personalPack() }
  }
  // any language on offer, as you see it: built in, the server's, yours
  let dlCode = $state(i18n.locale)
  async function loadLanguage() {
    const code = dlCode
    const mine = own?.code === code ? await personalPack() : null
    return { code, name: languages().find((l) => l.code === code)?.name || code, messages: { ...(await builtinAll(code)), ...(await serverPack(code)), ...mine } }
  }
  // ownIRC: how many of evrd's IRC texts (*status) you have in your own
  // translation's language: built in, the server's, yours
  let ownIRC = $state(null)
  $effect(() => {
    const p = own
    ownIRC = null
    if (p) ircCoverage(p.code).then((c) => { if (own === p) ownIRC = c }).catch(() => {})
  })
  async function ircCoverage(code) {
    const [en, base, server, mine] = await Promise.all([ircBuiltin('en'), ircBuiltin(code), serverPack(code), personalPack()])
    return coverage(en, base, server, mine)
  }

  // sessions
  let sessions = $state(null)
  async function loadSessions() { try { sessions = await api.sessions() } catch { sessions = [] } }
  onMount(loadSessions)
  async function revoke(s) {
    try { await api.revokeSession(s.id); await loadSessions(); toast(t('account.signedOut', { device: device(s.userAgent) })) } catch (e) { toast(e.message, 'bad') }
  }
  async function revokeOthers() {
    if (!(await confirmDialog({ title: t('account.signOutOthersTitle'), body: t('account.signOutOthersBody'), action: t('account.signOutOthersAction') }))) return
    try { await api.revokeOtherSessions(); await loadSessions(); toast(t('account.signedOutOthers')) } catch (e) { toast(e.message, 'bad') }
  }
  const others = $derived(sessions ? sessions.filter((s) => !s.current).length : 0)
  const myIP = $derived(sessions?.find((s) => s.current)?.ip || '')
  async function saveBouncer(settings) {
    session.me = await api.updateMe({ settings })
    return session.me.settings
  }


  async function deleteAccount() {
    if (!(await confirmDialog({ title: t('account.deleteTitle'), body: t('account.deleteBody', { count: me.usage.networks }), action: t('account.deleteAction'), typed: me.username }))) return
    try { await api.deleteMe(me.username); location.href = '/' } catch (e) { toast(e.message, 'bad') }
  }
</script>

<svelte:head><title>{t('account.title')} · evrd</title></svelte:head>

<div class="settings" style="display:grid;gap:var(--s6)">
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('account.heading')}</h1>
      <p>{t('account.intro')}</p>
    </div>
  </div>

  <div class="tabs" role="tablist" aria-label={t('account.sections')}>
    {#each TABS as [k, l], i}
      <button class="tab" role="tab" id="t-{k}" aria-selected={tab === k} aria-controls={k} tabindex={tab === k ? 0 : -1} onclick={() => pick(k)} onkeydown={(e) => tabKey(e, i)}>{t(l)}</button>
    {/each}
  </div>

  {#if tab === 'profile'}
    <div role="tabpanel" id="profile" aria-labelledby="t-profile" class="card">
      <div class="form">
      <form class="form" novalidate onsubmit={saveProfile}>
        <div class="panel-grid">
          <div class="od-stack"><h2>{t('account.identity')}</h2><p class="desc">{t('account.identityHelp')}</p></div>
          <div class="form-grid">
            <Field id="p-user" label={t('account.username')} help={t('account.usernameHelp')}><input class="input mono" id="p-user" value={me.username} readonly data-testid="acct-username" /></Field>
            <Field id="p-email" label={t('account.email')} help={me.sso ? t('account.emailSso', { sso: session.config.ssoLabel }) : t('account.emailHelp')} err={perr.email}>
              <input class="input" id="p-email" type="email" autocomplete="email" bind:value={email} readonly={me.sso} />
            </Field>
          </div>
        </div>
        <hr class="divider" />
        <div class="panel-grid">
          <div class="od-stack"><h2>{t('account.defaults')}</h2><p class="desc">{t('account.defaultsHelp')}</p></div>
          <div class="form-grid">
            <Field id="p-nick" label={t('account.nick')} required err={perr.nick}><input class="input mono" id="p-nick" spellcheck="false" bind:value={nick} /></Field>
            <Field id="p-alt" label={t('account.altNick')} help={t('account.altNickHelp')} err={perr.alt}><input class="input mono" id="p-alt" spellcheck="false" bind:value={alt} /></Field>
            <Field id="p-real" label={t('account.realName')} help={t('account.realNameHelp')} span><input class="input" id="p-real" bind:value={real} /></Field>
            <Field id="p-lang" label={t('account.language')} help={t('account.languageHelp')} span><Select id="p-lang" bind:value={lang} options={langOptions} testid="lang-select" /></Field>
            <div class="check-row span-2">
              <span class="od-field"><span id="away-l">{t('account.autoAway')}</span><span>{t('account.autoAwayHelp')}</span></span>
              <Switch checked={away} labelledby="away-l" onchange={(v) => (away = v)} />
            </div>
          </div>
        </div>
        <div class="form-foot"><button class="btn btn-primary" type="submit" disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('account.saving')}{:else}<Icon name="check" />{t('account.saveProfile')}{/if}</button></div>
      </form>
      <hr class="divider" />
      <div class="panel-grid" data-testid="own-translation">
        <div class="od-stack"><h2>{t('account.ownTranslation')}</h2><p class="desc">{t('account.ownTranslationHelp')} <a href="https://github.com/4evrd/evrd/blob/main/docs/translations.md" target="_blank" rel="noreferrer">{t('packs.guide')}</a></p></div>
        <div class="od-stack" style="--od-gap:12px;align-items:flex-start">
          <p class="desc" data-testid="own-status">{own ? t('account.ownCurrent', { name: own.name, code: own.code, time: ago(own.updatedAt) }) : t('account.ownNone')}</p>
          {#if ownIRC}<p class="desc" data-testid="own-irc">{t('packs.ircCoverage', { have: ownIRC.have, total: ownIRC.total })}</p>{/if}
          <div class="od-cluster" style="gap:8px">
            <PackUpload id="own-upload" label={own ? t('account.ownReplace') : t('account.ownUpload')} code={own?.code || i18n.locale} save={saveOwn} />
            <PackDownload id="own-template" label={t('packs.template')} ghost />
            {#if own}
              <PackDownload id="own-download" label={t('account.ownDownload')} ghost load={loadOwn} />
              <button class="btn btn-danger-ghost btn-sm" type="button" onclick={removeOwn}><Icon name="trash" />{t('account.ownRemove')}</button>
            {/if}
          </div>
          <div class="od-cluster" style="gap:8px" data-testid="download-language">
            <span class="desc">{t('account.downloadLanguage')}</span>
            <div style="min-width:180px"><Select id="dl-lang" bind:value={dlCode} options={languages().map((l) => ({ value: l.code, label: l.name, sub: l.code }))} testid="dl-lang" /></div>
            <PackDownload id="dl-language" label={t('account.downloadLanguageButton')} ghost load={loadLanguage} />
          </div>
        </div>
      </div>
      </div>
    </div>
  {:else if tab === 'bouncer'}
    <div role="tabpanel" id="bouncer" aria-labelledby="t-bouncer" class="card">
      <BouncerSettings value={me.settings} save={saveBouncer} {myIP} />
    </div>
  {:else if tab === 'security'}
    <div role="tabpanel" id="security" aria-labelledby="t-security" class="card">
      <div class="panel-grid">
        <div class="od-stack"><h2>{t('account.password')}</h2><p class="desc">{me.hasPassword ? t('account.passwordHelp') : t('account.passwordSso', { sso: session.config.ssoLabel })}</p></div>
        {#if me.hasPassword}<ChangePassword username={me.username} />{:else}<div><a class="btn" href="/clients"><Icon name="key" />{t('account.clientPasswords')}</a></div>{/if}
      </div>
      <SecondFactor />
    </div>
  {:else if tab === 'sessions'}
    <div role="tabpanel" id="sessions" aria-labelledby="t-sessions" class="card">
      <div class="section-head">
        <div class="od-stack"><h2>{t('account.signedIn')}</h2><p><Msg key="account.signedInHelp">{#snippet link()}<a href="/clients">{t('account.ircClientsLink')}</a>{/snippet}</Msg></p></div>
        <button class="btn btn-danger-ghost btn-sm" type="button" disabled={!others} onclick={revokeOthers}>{t('account.signOutOthers')}</button>
      </div>
      <div class="rows">
        {#if !sessions}<div class="skel"></div>{:else}
          {#each sessions as s (s.id)}
            <div class="row sess-row">
              <span class="sic"><Icon name={/iPhone|Android/.test(s.userAgent || '') ? 'message' : 'monitor'} /></span>
              <span class="od-field"><span class="name">{device(s.userAgent)}</span><span class="sub">{s.ip || t('account.unknownAddress')} · {s.method === 'oidc' ? session.config.ssoLabel : t('account.methodPassword')} · {s.current ? t('account.thisSession') : t('account.active', { ago: ago(s.lastSeenAt) })}</span></span>
              {#if s.current}<span class="chip ok">{t('account.thisDevice')}</span>{:else}<button class="btn btn-ghost btn-sm" type="button" aria-label={t('account.signOutDevice', { device: device(s.userAgent) })} onclick={() => revoke(s)}>{t('account.signOut')}</button>{/if}
            </div>
          {/each}
        {/if}
      </div>
    </div>
  {:else}
    <div role="tabpanel" id="danger" aria-labelledby="t-danger" class="card danger-card">
      <div class="panel-grid">
        <div class="od-stack"><h2>{t('account.deleteHeading')}</h2><p class="desc">{t('account.deleteHelp')}</p></div>
        <div class="od-stack" style="--od-gap:16px;align-items:flex-start">
          {#if me.role === 'admin'}
            {#if me.otherAdmins?.length}<p class="desc" style="color:var(--fg)"><Msg key="account.adminsStay" vars={{ count: me.otherAdmins.length }}>{#snippet admins()}{#each me.otherAdmins as a, i}<span class="code">{a}</span>{i < me.otherAdmins.length - 1 ? ', ' : ''}{/each}{/snippet}</Msg></p>
            {:else}<div class="callout warn"><Icon name="alert" /><span><b>{t('account.onlyAdmin')}</b> {t('account.onlyAdminHelp')}</span></div>{/if}
          {/if}
          <button class="btn btn-danger" type="button" onclick={deleteAccount} disabled={me.role === 'admin' && !me.otherAdmins?.length}><Icon name="trash" />{t('account.deleteMine')}</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .panel-grid { display: grid; grid-template-columns: minmax(0, 280px) minmax(0, 1fr); gap: var(--s7); }
  .panel-grid :global(.od-stack) { --od-gap: 6px; }
  :global(.form-foot) { display: flex; justify-content: flex-end; gap: var(--s2); }
  .sess-row { --cols: 36px minmax(0, 1fr) auto; }
  .sic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--muted); }
  .danger-card { border-color: oklch(68% 0.18 27 / 0.35); background: linear-gradient(180deg, var(--glass), oklch(68% 0.18 27 / 0.06)); }
  @media (max-width: 959px) { .panel-grid { grid-template-columns: minmax(0, 1fr); gap: var(--s4); } }
</style>
