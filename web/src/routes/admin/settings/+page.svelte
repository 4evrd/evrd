<script>
  // Admin → Settings: the server settings from evrd.conf, editable here.
  // Values are saved in the database (every node follows within seconds);
  // the environment still wins on its node.
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Select from '$lib/components/Select.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago } from '$lib/format.js'

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
  const SOURCE = { default: 'Default', file: 'evrd.conf', ui: 'Set here', env: 'Environment' }
  const CHOICE = { acme: 'Let’s Encrypt (automatic)', selfsigned: 'Self-signed', files: 'Certificate files' }

  // HTTPS & TLS: show only the fields for the certificate source picked.
  const tlsSource = $derived(data ? valueOf(data.fields.find((f) => f.key === 'tls') || { key: 'tls', value: '' }) : '')
  function shown(f) {
    if (f.key.startsWith('acme_')) return tlsSource === 'acme'
    if (f.key === 'tls_cert' || f.key === 'tls_key') return tlsSource === 'files'
    return true
  }
  const DAY = 86400
  function expiry(t) {
    const days = Math.floor((t - Date.now() / 1000) / DAY)
    return days < 0 ? 'expired' : days === 0 ? 'expires today' : `expires in ${days} day${days === 1 ? '' : 's'}`
  }

  function check(f, v) {
    if (f.kind === 'int' && !/^\d+$/.test(v.trim())) return 'A whole number, 0 or more.'
    if (f.kind === 'float' && !/^\d+(\.\d+)?$/.test(v.trim())) return 'A number, 0 or more.'
    if (f.kind === 'duration' && !/^(\d+(\.\d+)?(h|m|s|ms))+$/.test(v.trim())) return 'A duration like 720h, 30m or 15s.'
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
      toast(restart.length ? `Saved. ${restart.map((f) => f.label).join(', ')} take${restart.length === 1 ? 's' : ''} effect after a restart.` : 'Saved — applied on every node')
      api.config().then((c) => (session.config = c)).catch(() => {})
    } catch (x) { toast(x.message, 'bad') } finally { saving = false }
  }
  async function reset(f) {
    const body = data.writeConf
      ? 'This removes the value set here, and its directive from each node’s evrd.conf, so evrd goes back to the default.'
      : `This removes the value set here, so evrd goes back to ${f.inFile ? `evrd.conf’s value${f.kind === 'secret' ? '' : ` (${f.fileValue || 'empty'})`}` : `the default${f.kind === 'secret' || f.fileValue === '' ? '' : ` (${f.fileValue})`}`}.`
    if (!(await confirmDialog({ title: `Reset ${f.label}?`, body, action: 'Reset', danger: false }))) return
    try { data = await api.admin.saveSettings({}, [f.key]); const { [f.key]: _, ...rest } = draft; draft = rest; toast(`Reset ${f.label}`) } catch (x) { toast(x.message, 'bad') }
  }
  function discard() { draft = {}; errs = {} }

  // The network directory: fetch it again now.
  let checking = $state(false)
  async function refreshDirectory() {
    checking = true
    try {
      const d = await api.admin.refreshDirectory()
      data = { ...data, directory: d }
      toast(d.error ? `Couldn’t fetch the directory: ${d.error}` : `Fetched the directory: ${d.networks} networks`, d.error ? 'bad' : undefined)
    } catch (x) { toast(x.message, 'bad') } finally { checking = false }
  }
</script>

<svelte:head><title>Settings · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>Admins only.</b> Your account doesn’t have access to server settings.</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>Settings</h1>
      <p>Server settings from evrd.conf. Changes here are saved in the database and reach every node within seconds; the environment still wins on its node.</p>
    </div>
  </div>

  {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>Couldn’t load settings: {err}</span></div>{/if}
  {#if !data}
    {#if !err}<div class="skel" style="height:200px"></div>{/if}
  {:else}
    <div class="callout" style="margin-top:var(--s5)">
      <Icon name="info" />
      <span>
        {#if data.writeConf}
          <b>Writing to evrd.conf is on</b> (<span class="code">settings_write_conf yes</span>): each node also writes these values into its own config file.
          {#if data.clustered}<b>This is a cluster</b> — usually leave it off and let the database hold the settings.{/if}
        {:else}
          Values set here live in the database only; evrd.conf keeps its own values as the fallback. Turn on <span class="code">settings_write_conf yes</span> in evrd.conf to also write them there.
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
                  <span class="name">{d.source ? `${d.networks} network${d.networks === 1 ? '' : 's'}` : 'Off'}</span>
                  <span class="sub">
                    {#if !d.source}Users type every network’s details themselves.
                    {:else}{d.fetchedAt ? `Fetched ${ago(d.fetchedAt)}` : 'Not fetched yet'}{#if d.updated} · list updated {ago(d.updated)}{/if}{#if d.error} · last try failed: {d.error}{d.networks ? ' — keeping the last good copy' : ''}{/if}{#if d.skipped?.length} · {d.skipped.length} entr{d.skipped.length === 1 ? 'y' : 'ies'} skipped{/if}{/if}
                  </span>
                </span>
                {#if d.source}
                  {#if d.error}<span class="chip bad">Failed</span>{:else if d.fetchedAt}<span class="chip ok">OK</span>{:else}<span class="chip warn">Pending</span>{/if}
                  <button class="btn btn-sm" type="button" onclick={refreshDirectory} disabled={checking}><Icon name="refresh" />Check now</button>
                {/if}
              </div>
            </div>
          {/if}
          {#if g.name === 'HTTPS & TLS' && data.tls}
            {@const t = data.tls}
            <div class="callout tls-who" data-testid="tls-who">
              <Icon name={t.httpsListen ? 'lock' : 'info'} />
              <span>
                {#if t.httpsListen}
                  <b>evrd serves HTTPS itself</b> on <span class="code">{t.httpsListen}</span>{#if t.httpListen}, and sends plain HTTP on <span class="code">{t.httpListen}</span> there{/if} — with the certificate below, which the IRC TLS ports use too.
                {:else}
                  <b>A proxy in front of evrd handles HTTPS</b> for the web app (<span class="code">https_listen</span> isn’t set in evrd.conf), so the certificate below is for the IRC TLS ports.
                  To let evrd serve HTTPS itself, set <span class="code">https_listen :443</span> (and <span class="code">http_listen :80</span>) in evrd.conf and restart.
                {/if}
              </span>
            </div>
            {#if t.report?.certs?.length}
              <div class="rows certs" data-testid="tls-certs">
                {#each t.report.certs as c}
                  <div class="row" style="--cols:minmax(0,1fr) auto">
                    <span class="od-field">
                      <span class="name mono">{c.host}</span>
                      <span class="sub">
                        {#if c.error}{c.error}
                        {:else if c.pending}Not got yet — the first visit (or evrd, within minutes) asks Let’s Encrypt for it
                        {:else}{c.issuer}{#if c.names?.length} · {c.names.join(', ')}{/if}{/if}
                      </span>
                    </span>
                    {#if c.error}<span class="chip bad">Failed</span>
                    {:else if c.pending}<span class="chip warn">Pending</span>
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
                  <label for="s-{f.key}" class="name">{f.label} <span class="code key">{f.key}</span></label>
                  {#if f.help}<p class="help">{f.help}</p>{/if}
                  <div class="od-cluster chips-s">
                    <span class="chip src-{f.source}">{SOURCE[f.source]}{f.source === 'env' && f.env ? ` · ${f.env}` : ''}</span>
                    {#if f.source === 'ui' && f.updatedBy}<span class="faint" style="font-size:var(--fs-xs)">by {f.updatedBy} {ago(f.updatedAt)}</span>{/if}
                    {#if f.restartPending}<span class="chip warn"><Icon name="refresh" />Restart needed</span>{:else if f.restart}<span class="faint" style="font-size:var(--fs-xs)">applies after a restart</span>{/if}
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
                      placeholder={f.kind === 'secret' ? (f.set ? '•••••••• (unchanged)' : 'not set') : f.kind === 'list' ? 'none' : ''}
                      value={valueOf(f)} oninput={(e) => edit(f, e.currentTarget.value)} aria-invalid={!!errs[f.key]} />
                  {/if}
                  {#if errs[f.key]}<span class="err-t">{errs[f.key]}</span>{/if}
                  {#if f.source === 'ui'}<button class="btn btn-ghost btn-sm" type="button" onclick={() => reset(f)}>Reset</button>{/if}
                </div>
              </div>
            {/each}
          </div>
        </section>
      {/each}

      <section class="card" aria-labelledby="boot-h">
        <h2 id="boot-h" class="group-h">Startup (evrd.conf / environment only)</h2>
        <p class="desc" style="margin-bottom:var(--s3)">evrd needs these before it can read the database, and some differ per node. Change them in the config file or environment and restart.</p>
        <div class="kv-list">
          {#each data.bootstrap as b}<div class="kv"><div class="od-field"><span class="k">{b.key}</span><span class="v">{b.value || '—'}</span></div></div>{/each}
        </div>
      </section>

      {#if data.writeConf}
        <section class="card" aria-labelledby="conf-h">
          <h2 id="conf-h" class="group-h">Config file writes</h2>
          <div class="rows">
            {#each data.confs as c}
              <div class="row" style="--cols:minmax(0,1fr) auto">
                <span class="od-field"><span class="name">{c.node}</span><span class="sub mono">{c.path} · {c.error ? c.error : 'written ' + ago(c.wroteAt)}</span></span>
                {#if c.error}<span class="chip bad">Failed</span>{:else}<span class="chip ok">OK</span>{/if}
              </div>
            {:else}<p class="desc">No node has written its file yet.</p>{/each}
          </div>
        </section>
      {/if}
    </div>

    {#if dirty}
      <div class="save-bar">
        <span class="faint">{Object.keys(draft).length} change{Object.keys(draft).length === 1 ? '' : 's'}</span>
        <span style="flex:1"></span>
        <button class="btn btn-ghost" type="button" onclick={discard}>Discard</button>
        <button class="btn btn-primary" type="button" onclick={save} disabled={saving}>{#if saving}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />Save changes{/if}</button>
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
