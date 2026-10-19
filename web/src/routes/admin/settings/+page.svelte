<script>
  // Admin → Settings: the server settings from evrd.conf, editable here.
  // Values are saved in the database (every node follows within seconds);
  // the environment still wins on its node.
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Select from '$lib/components/Select.svelte'
  import Encrypted from '$lib/components/Encrypted.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago } from '$lib/format.js'
  import Msg from '$lib/components/Msg.svelte'
  import { t } from '$lib/i18n.svelte.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  let data = $state(null)
  let err = $state('')
  let draft = $state({})      // key -> edited value (string)
  let errs = $state({})
  let saving = $state(false)

  async function load() {
    try { data = await api.admin.settings(); draft = {}; err = '' } catch (e) { err = e.message }
  }
  onMount(() => { if (isAdmin) load() })

  const groups = $derived.by(() => {
    const out = []
    for (const f of data?.fields || []) {
      let g = out.find((x) => x.name === f.group)
      if (!g) out.push((g = { name: f.group, fields: [] }))
      g.fields.push(f)
    }
    return out
  })
  const dirty = $derived(Object.keys(draft).length > 0)
  const valueOf = (f) => (f.key in draft ? draft[f.key] : f.kind === 'secret' ? '' : f.value)
  function edit(f, v) {
    if (f.kind !== 'secret' && v === f.value) { const { [f.key]: _, ...rest } = draft; draft = rest } else draft = { ...draft, [f.key]: v }
    errs = { ...errs, [f.key]: '' }
  }
  const SOURCE = $derived({ default: t('adminSettings.sourceDefault'), file: 'evrd.conf', ui: t('adminSettings.sourceUi'), env: t('adminSettings.sourceEnv') })
  const CHOICE = $derived({ acme: t('adminSettings.choiceAcme'), selfsigned: t('adminSettings.choiceSelfSigned'), files: t('adminSettings.choiceFiles') })

  // HTTPS & TLS: show only the fields for the certificate source picked.
  const tlsSource = $derived(data ? valueOf(data.fields.find((f) => f.key === 'tls') || { key: 'tls', value: '' }) : '')
  function shown(f) {
    if (f.key.startsWith('acme_')) return tlsSource === 'acme'
    if (f.key === 'tls_cert' || f.key === 'tls_key') return tlsSource === 'files'
    return true
  }
  const DAY = 86400
  function expiry(at) {
    const days = Math.floor((at - Date.now() / 1000) / DAY)
    return days < 0 ? t('adminSettings.expired') : days === 0 ? t('adminSettings.expiresToday') : t('adminSettings.expiresIn', { count: days })
  }

  function check(f, v) {
    if (f.kind === 'int' && !/^\d+$/.test(v.trim())) return t('adminSettings.errInt')
    if (f.kind === 'float' && !/^\d+(\.\d+)?$/.test(v.trim())) return t('adminSettings.errFloat')
    if (f.kind === 'duration' && !/^(\d+(\.\d+)?(h|m|s|ms))+$/.test(v.trim())) return t('adminSettings.errDuration')
    return ''
  }
  async function save() {
    const e = {}
    for (const f of data.fields) if (f.key in draft) e[f.key] = check(f, draft[f.key])
    errs = e
    if (Object.values(e).some(Boolean)) { document.querySelector('[aria-invalid="true"]')?.focus(); return }
    const restart = data.fields.filter((f) => f.key in draft && f.restart)
    saving = true
    try {
      data = await api.admin.saveSettings(draft, [])
      draft = {}
      toast(restart.length ? t('adminSettings.savedRestart', { count: restart.length, labels: restart.map((f) => f.label).join(', ') }) : t('adminSettings.saved'))
      api.config().then((c) => (session.config = c)).catch(() => {})
    } catch (x) { toast(x.message, 'bad') } finally { saving = false }
  }
  async function reset(f) {
    const body = data.writeConf
      ? t('adminSettings.resetBodyConf')
      : f.inFile
        ? (f.kind === 'secret' ? t('adminSettings.resetToFile') : t('adminSettings.resetToFileValue', { value: f.fileValue || t('adminSettings.empty') }))
        : (f.kind === 'secret' || f.fileValue === '' ? t('adminSettings.resetToDefault') : t('adminSettings.resetToDefaultValue', { value: f.fileValue }))
    if (!(await confirmDialog({ title: t('adminSettings.resetTitle', { label: f.label }), body, action: t('adminSettings.reset'), danger: false }))) return
    try { data = await api.admin.saveSettings({}, [f.key]); const { [f.key]: _, ...rest } = draft; draft = rest; toast(t('adminSettings.resetDone', { label: f.label })) } catch (x) { toast(x.message, 'bad') }
  }
  function discard() { draft = {}; errs = {} }

  // The network directory: fetch it again now.
  let checking = $state(false)
  async function refreshDirectory() {
    checking = true
    try {
      const d = await api.admin.refreshDirectory()
      data = { ...data, directory: d }
      toast(d.error ? t('adminSettings.dirFetchFailed', { error: d.error }) : t('adminSettings.dirFetched', { count: d.networks }), d.error ? 'bad' : undefined)
    } catch (x) { toast(x.message, 'bad') } finally { checking = false }
  }
</script>

<svelte:head><title>{t('adminSettings.pageTitle')}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('adminSettings.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('adminSettings.heading')}</h1>
      <p>{t('adminSettings.intro')}</p>
    </div>
  </div>

  {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{t('adminSettings.loadFailed', { error: err })}</span></div>{/if}
  {#if !data}
    {#if !err}<div class="skel" style="height:200px"></div>{/if}
  {:else}
    <div class="callout" style="margin-top:var(--s5)">
      <Icon name="info" />
      <span>
        {#if data.writeConf}
          <Msg key="adminSettings.writeConfOn">{#snippet on()}<b>{t('adminSettings.writeConfOnBold')}</b>{/snippet}{#snippet setting()}<span class="code">settings_write_conf yes</span>{/snippet}</Msg>
          {#if data.clustered}<Msg key="adminSettings.writeConfCluster">{#snippet cluster()}<b>{t('adminSettings.writeConfClusterBold')}</b>{/snippet}</Msg>{/if}
        {:else}
          <Msg key="adminSettings.writeConfOff">{#snippet setting()}<span class="code">settings_write_conf yes</span>{/snippet}</Msg>
        {/if}
      </span>
    </div>

    <div class="sections">
      {#each groups as g}
        <section class="card" aria-labelledby="g-{g.name}">
          <h2 id="g-{g.name}" class="group-h">{g.name}</h2>
          {#if g.name === 'Network directory' && data.directory}
            {@const d = data.directory}
            <div class="rows dir-status" data-testid="directory-status">
              <div class="row" style="--cols:minmax(0,1fr) auto auto">
                <span class="od-field">
                  <span class="name">{d.source ? t('admin.nNetworks', { count: d.networks }) : t('admin.off')}</span>
                  <span class="sub">
                    {#if !d.source}{t('adminSettings.dirManual')}
                    {:else}{d.fetchedAt ? t('adminSettings.fetched', { time: ago(d.fetchedAt) }) : t('adminSettings.notFetched')}{#if d.updated} · {t('adminSettings.listUpdated', { time: ago(d.updated) })}{/if}{#if d.error} · {t('adminSettings.lastTryFailed', { error: d.error })}{d.networks ? ' — ' + t('adminSettings.keepingLastGood') : ''}{/if}{#if d.skipped?.length} · {t('adminSettings.nSkipped', { count: d.skipped.length })}{/if}{/if}
                  </span>
                </span>
                {#if d.source}
                  {#if d.error}<span class="chip bad">{t('adminSettings.failed')}</span>{:else if d.fetchedAt}<span class="chip ok">{t('admin.ok')}</span>{:else}<span class="chip warn">{t('adminSettings.pending')}</span>{/if}
                  <button class="btn btn-sm" type="button" onclick={refreshDirectory} disabled={checking}><Icon name="refresh" />{t('adminSettings.checkNow')}</button>
                {/if}
              </div>
            </div>
          {/if}
          {#if g.name === 'HTTPS & TLS' && data.tls}
            {@const tl = data.tls}
            <div class="callout tls-who" data-testid="tls-who">
              <Icon name={tl.httpsListen ? 'lock' : 'info'} />
              <span>
                {#if tl.httpsListen}
                  <Msg key={tl.httpListen ? 'adminSettings.tlsSelfHttp' : 'adminSettings.tlsSelf'}>{#snippet self()}<b>{t('adminSettings.tlsSelfBold')}</b>{/snippet}{#snippet https()}<span class="code">{tl.httpsListen}</span>{/snippet}{#snippet http()}<span class="code">{tl.httpListen}</span>{/snippet}</Msg>
                {:else}
                  <Msg key="adminSettings.tlsProxy">{#snippet proxy()}<b>{t('adminSettings.tlsProxyBold')}</b>{/snippet}{#snippet setting()}<span class="code">https_listen</span>{/snippet}</Msg>
                  <Msg key="adminSettings.tlsProxyHow">{#snippet https()}<span class="code">https_listen :443</span>{/snippet}{#snippet http()}<span class="code">http_listen :80</span>{/snippet}</Msg>
                {/if}
              </span>
            </div>
            {#if tl.report?.certs?.length}
              <div class="rows certs" data-testid="tls-certs">
                {#each tl.report.certs as c}
                  <div class="row" style="--cols:minmax(0,1fr) auto">
                    <span class="od-field">
                      <span class="name mono">{c.host}</span>
                      <span class="sub">
                        {#if c.error}{c.error}
                        {:else if c.pending}{t('adminSettings.certPending')}
                        {:else}{c.issuer}{#if c.names?.length} · {c.names.join(', ')}{/if}{/if}
                      </span>
                    </span>
                    {#if c.error}<span class="chip bad">{t('adminSettings.failed')}</span>
                    {:else if c.pending}<span class="chip warn">{t('adminSettings.pending')}</span>
                    {:else if c.notAfter}<span class="chip" class:ok={c.notAfter - Date.now() / 1000 > 14 * DAY} class:warn={c.notAfter - Date.now() / 1000 <= 14 * DAY}>{expiry(c.notAfter)}</span>{/if}
                  </div>
                {/each}
              </div>
            {/if}
          {/if}
          <div class="rows-s">
            {#each g.fields.filter(shown) as f (f.key)}
              {@const locked = f.source === 'env'}
              <div class="setting" data-testid="setting-{f.key}">
                <div class="od-stack meta">
                  <label for="s-{f.key}" class="name">{f.label} <span class="code key">{f.key}</span></label>{#if f.kind === 'secret'}<Encrypted />{/if}
                  {#if f.help}<p class="help">{f.help}</p>{/if}
                  <div class="od-cluster chips-s">
                    <span class="chip src-{f.source}">{SOURCE[f.source]}{f.source === 'env' && f.env ? ` · ${f.env}` : ''}</span>
                    {#if f.source === 'ui' && f.updatedBy}<span class="faint" style="font-size:var(--fs-xs)">{t('adminSettings.byWhen', { user: f.updatedBy, time: ago(f.updatedAt) })}</span>{/if}
                    {#if f.restartPending}<span class="chip warn"><Icon name="refresh" />{t('adminSettings.restartNeeded')}</span>{:else if f.restart}<span class="faint" style="font-size:var(--fs-xs)">{t('adminSettings.appliesAfterRestart')}</span>{/if}
                  </div>
                </div>
                <div class="control">
                  {#if f.kind === 'choice'}
                    <Select id="s-{f.key}" disabled={locked} value={valueOf(f)} onchange={(v) => edit(f, v)} options={f.options.map((o) => ({ value: o, label: CHOICE[o] || o }))} />
                  {:else if f.kind === 'bool'}
                    <Switch id="s-{f.key}" checked={valueOf(f) === 'yes'} label={f.label} disabled={locked} onchange={(v) => edit(f, v ? 'yes' : 'no')} />
                  {:else}
                    <input class="input" class:mono={f.kind !== 'string' || f.key.includes('url') || f.key.includes('host')} id="s-{f.key}"
                      type={f.kind === 'secret' ? 'password' : 'text'} autocomplete="off" spellcheck="false" disabled={locked}
                      inputmode={f.kind === 'int' ? 'numeric' : f.kind === 'float' ? 'decimal' : undefined}
                      placeholder={f.kind === 'secret' ? (f.set ? t('adminSettings.secretUnchanged') : t('adminSettings.notSet')) : f.kind === 'list' ? t('adminSettings.none') : ''}
                      value={valueOf(f)} oninput={(e) => edit(f, e.currentTarget.value)} aria-invalid={!!errs[f.key]} />
                  {/if}
                  {#if errs[f.key]}<span class="err-t">{errs[f.key]}</span>{/if}
                  {#if f.source === 'ui'}<button class="btn btn-ghost btn-sm" type="button" onclick={() => reset(f)}>{t('adminSettings.reset')}</button>{/if}
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/each}

      <section class="card" aria-labelledby="boot-h">
        <h2 id="boot-h" class="group-h">{t('adminSettings.startup')}</h2>
        <p class="desc" style="margin-bottom:var(--s3)">{t('adminSettings.startupHelp')}</p>
        <div class="kv-list">
          {#each data.bootstrap as b}<div class="kv"><div class="od-field"><span class="k">{b.key}</span><span class="v">{b.value || '—'}</span></div></div>{/each}
        </div>
      </section>

      {#if data.writeConf}
        <section class="card" aria-labelledby="conf-h">
          <h2 id="conf-h" class="group-h">{t('adminSettings.confWrites')}</h2>
          <div class="rows">
            {#each data.confs as c}
              <div class="row" style="--cols:minmax(0,1fr) auto">
                <span class="od-field"><span class="name">{c.node}</span><span class="sub mono">{c.path} · {c.error ? c.error : t('adminSettings.written', { time: ago(c.wroteAt) })}</span></span>
                {#if c.error}<span class="chip bad">{t('adminSettings.failed')}</span>{:else}<span class="chip ok">{t('admin.ok')}</span>{/if}
              </div>
            {:else}<p class="desc">{t('adminSettings.noConfs')}</p>{/each}
          </div>
        </section>
      {/if}
    </div>

    {#if dirty}
      <div class="save-bar">
        <span class="faint">{t('adminSettings.nChanges', { count: Object.keys(draft).length })}</span>
        <span style="flex:1"></span>
        <button class="btn btn-ghost" type="button" onclick={discard}>{t('adminSettings.discard')}</button>
        <button class="btn btn-primary" type="button" onclick={save} disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('adminUsers.saving')}{:else}<Icon name="check" />{t('adminSettings.saveChanges')}{/if}</button>
      </div>
    {/if}
  {/if}
{/if}

<style>
  .sections { display: grid; gap: var(--s5); margin-top: var(--s5); }
  .group-h { font-size: var(--fs-md); font-weight: 500; margin-bottom: var(--s4); }
  .rows-s { display: grid; }
  .setting { display: grid; grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr); gap: var(--s5); padding: var(--s4) 0; border-top: 1px solid var(--line-soft); align-items: center; }
  .setting:first-child { border-top: 0; padding-top: 0; }
  .meta { --od-gap: 4px; }
  .name { font-weight: 500; font-size: var(--fs-sm); }
  .key { margin-left: 6px; font-size: var(--fs-2xs); }
  .chips-s { gap: 8px; margin-top: 4px; }
  .control { display: flex; align-items: center; gap: var(--s2); justify-content: flex-end; flex-wrap: wrap; }
  .control .input { flex: 1; min-width: 0; }
  .err-t { flex-basis: 100%; color: var(--bad); font-size: var(--fs-xs); text-align: right; }
  .chip.src-ui { color: var(--primary); }
  .tls-who { margin-bottom: var(--s4); }
  .certs, .dir-status { margin-bottom: var(--s4); }
  .control :global(.dd) { flex: 1; min-width: 0; }
  .chip.src-env { color: var(--warn); }
  .save-bar { position: sticky; bottom: var(--s4); z-index: 5; display: flex; align-items: center; gap: var(--s2); margin-top: var(--s5); padding: var(--s3) var(--s4); border-radius: var(--r-card); background: var(--bg-2); border: 1px solid var(--line); box-shadow: var(--shadow); }
  @media (max-width: 799px) { .setting { grid-template-columns: minmax(0, 1fr); gap: var(--s2); } .control { justify-content: flex-start; } }
</style>
