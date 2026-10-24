<script>
  // Import networks from a file: an evrd export, or a ZNC config (whole or
  // a part). The server reads it (preview), you pick the ZNC user and the
  // networks, then it imports them. Passwords never come back to the page.
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import Select from '$lib/components/Select.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { refreshNets, refreshMe } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let input = $state()
  let text = $state('') // the file's text, as read or pasted
  let filename = $state('')
  let pasted = $state('')
  let preview = $state(null) // { format, users: [{ name, networks: [...] }] }
  let err = $state('')
  let busy = $state(false)
  let user = $state('')
  let picked = $state({}) // network name → true
  let existing = $state('skip')
  let connect = $state(false)
  let results = $state(null)

  const users = $derived(preview?.users || [])
  const nets = $derived(users.find((u) => u.name === user)?.networks || [])
  const chosen = $derived(nets.filter((n) => picked[n.name]))
  const userLabel = (u) => u.name || t('transfer.noUser')

  // a new user (or file) picks the networks it doesn't have yet
  function pickUser(name) {
    user = name
    const list = users.find((u) => u.name === name)?.networks || []
    picked = Object.fromEntries(list.map((n) => [n.name, !n.exists]))
  }

  async function read(body, name) {
    busy = true
    err = ''
    results = null
    try {
      preview = await api.previewImport(body)
      text = body
      filename = name
      pickUser(preview.users[0]?.name ?? '')
    } catch (e) { err = e.message; preview = null } finally { busy = false }
  }

  async function chose(e) {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ''
    if (f) read(await f.text(), f.name)
  }

  function reset() {
    preview = null
    results = null
    text = filename = err = ''
  }

  async function go() {
    busy = true
    err = ''
    try {
      const out = await api.importNetworks({ text, user, networks: chosen.map((n) => n.name), existing, connect })
      results = out.results
      refreshNets()
      refreshMe()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>

<svelte:head><title>{t('transfer.title')} · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <a class="back" href="/networks"><Icon name="chevron-left" class="i-sm" />{t('networks.title')}</a>
    <h1>{t('transfer.title')}</h1>
    <p>{t('transfer.intro')}</p>
  </div>
</div>

<div class="imp enter">
  {#if !preview}
    <section class="card od-stack" data-testid="import-pick">
      <input class="file" type="file" accept=".yaml,.yml,.conf,.txt,text/plain" bind:this={input} onchange={chose} data-testid="import-file" tabindex="-1" aria-hidden="true" />
      <div><button class="btn btn-primary" type="button" onclick={() => input.click()} disabled={busy}><Icon name="upload" />{t('transfer.choose')}</button></div>
      <Field id="imp-paste" label={t('transfer.paste')}>
        <textarea class="textarea mono" id="imp-paste" rows="8" spellcheck="false" placeholder={t('transfer.pastePlaceholder')} bind:value={pasted} data-testid="import-paste"></textarea>
      </Field>
      <div><button class="btn" type="button" onclick={() => read(pasted, '')} disabled={busy || !pasted.trim()} data-testid="import-read">{#if busy}<span class="spinner" aria-hidden="true"></span>{t('transfer.reading')}{:else}{t('transfer.read')}{/if}</button></div>
      <p class="help"><Icon name="lock" class="i-sm" /> {t('transfer.secrets')}</p>
    </section>
  {:else if results}
    <section class="card od-stack" data-testid="import-results">
      <h2>{t('transfer.done')}</h2>
      <ul class="rows">
        {#each results as r (r.name)}
          <li class="row" data-testid="import-result" data-result={r.result}>
            <Icon name={r.result === 'failed' ? 'alert' : r.result === 'skipped' ? 'info' : 'check'} class="i-sm {r.result}" />
            <div class="od-field">
              <span><b>{r.name}</b> · {t('transfer.result.' + r.result)}</span>
              {#if r.error}<span class="bad-text">{r.error}</span>{/if}
              {#each r.notes as note}<span class="note"><Icon name="alert" class="i-sm" />{note}</span>{/each}
            </div>
          </li>
        {/each}
      </ul>
      <div class="od-cluster"><a class="btn btn-primary" href="/networks">{t('transfer.toNetworks')}</a><button class="btn btn-ghost" type="button" onclick={reset}>{t('transfer.another')}</button></div>
    </section>
  {:else}
    <section class="card od-stack" data-testid="import-preview">
      <div class="src">
        <span class="chip"><Icon name={preview.format === 'znc' ? 'server' : 'disk'} />{t(preview.format === 'znc' ? 'transfer.formatZnc' : 'transfer.formatEvrd')}</span>
        {#if filename}<span class="mono faint od-truncate">{filename}</span>{/if}
        <button class="btn btn-ghost btn-sm" type="button" onclick={reset}>{t('transfer.another')}</button>
      </div>

      {#if users.length > 1}
        <Field id="imp-user" label={t('transfer.zncUser')} help={t('transfer.zncUserHelp')}>
          <Select id="imp-user" value={user} onchange={pickUser} testid="import-user"
            options={users.map((u) => ({ value: u.name, label: userLabel(u), sub: t('transfer.userNetworks', { count: u.networks.length }) }))} />
        </Field>
      {/if}

      <div class="section-head">
        <h2 id="imp-nets">{t('transfer.networks')}</h2>
        {#if nets.length > 1}
          <div class="od-cluster">
            <button class="btn btn-ghost btn-sm" type="button" onclick={() => (picked = Object.fromEntries(nets.map((n) => [n.name, true])))}>{t('transfer.all')}</button>
            <button class="btn btn-ghost btn-sm" type="button" onclick={() => (picked = {})}>{t('transfer.none')}</button>
          </div>
        {/if}
      </div>
      {#if !nets.length}
        <p class="faint">{t('transfer.empty')}</p>
      {:else}
        <ul class="rows" aria-labelledby="imp-nets">
          {#each nets as n (n.name)}
            <li>
              <label class="row pick" class:on={picked[n.name]} data-testid="import-network">
                <input type="checkbox" bind:checked={picked[n.name]} />
                <span class="od-field">
                  <span class="name"><b>{n.name}</b>
                    {#if n.exists}<span class="chip sm">{t('transfer.exists')}</span>{/if}
                    {#if !n.enabled}<span class="chip sm">{t('transfer.off')}</span>{/if}
                  </span>
                  <span class="mono faint">{[n.servers.join(', '), n.nick, t('transfer.channels', { count: n.channels })].filter(Boolean).join(' · ')}</span>
                  {#each n.notes as note}<span class="note"><Icon name="alert" class="i-sm" />{note}</span>{/each}
                </span>
              </label>
            </li>
          {/each}
        </ul>
      {/if}

      {#if chosen.some((n) => n.exists)}
        <div class="modes" role="radiogroup" aria-label={t('transfer.existing')}>
          <span class="caps">{t('transfer.existing')}</span>
          <label class="mode" class:on={existing === 'skip'}><input type="radio" name="imp-existing" value="skip" bind:group={existing} /><span class="od-field"><span>{t('transfer.skip')}</span><span>{t('transfer.skipHelp')}</span></span></label>
          <label class="mode" class:on={existing === 'update'}><input type="radio" name="imp-existing" value="update" bind:group={existing} data-testid="import-update" /><span class="od-field"><span>{t('transfer.update')}</span><span>{t('transfer.updateHelp')}</span></span></label>
        </div>
      {/if}

      <div class="toggle">
        <Switch checked={connect} labelledby="imp-connect" onchange={(v) => (connect = v)} />
        <span class="od-field"><span id="imp-connect">{t('transfer.connect')}</span><span class="faint">{t('transfer.connectHelp')}</span></span>
      </div>

      {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{err}</span></div>{/if}
      <div><button class="btn btn-primary" type="button" onclick={go} disabled={busy || !chosen.length} data-testid="import-go">{#if busy}<span class="spinner" aria-hidden="true"></span>{t('transfer.importing')}{:else}<Icon name="check" />{t('transfer.go', { count: chosen.length })}{/if}</button></div>
    </section>
  {/if}
  {#if err && !preview}<div class="callout bad" role="alert" data-testid="import-error"><Icon name="alert" /><span>{err}</span></div>{/if}
</div>

<style>
  .imp { display: grid; gap: var(--s4); max-width: 760px; margin-top: var(--s5); }
  .imp .od-stack { --od-gap: var(--s4); }
  .back { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: var(--fs-sm); }
  .file { display: none; }
  .help { display: flex; gap: var(--s2); align-items: flex-start; color: var(--muted); font-size: var(--fs-xs); }
  .help :global(.i) { flex: none; margin-top: 2px; }
  .src { display: flex; flex-wrap: wrap; align-items: center; gap: var(--s3); }
  .src .mono { min-width: 0; flex: 1; font-size: var(--fs-xs); }
  .section-head { display: flex; align-items: center; justify-content: space-between; gap: var(--s3); }
  .rows { display: grid; gap: var(--s2); margin: 0; padding: 0; list-style: none; }
  .row { display: flex; gap: var(--s3); align-items: flex-start; padding: var(--s3); border: 1px solid var(--line-soft); border-radius: var(--r-row); font-size: var(--fs-sm); }
  .row .od-field { --od-gap: 4px; min-width: 0; }
  .row .mono { font-size: var(--fs-xs); overflow-wrap: anywhere; }
  .pick { cursor: pointer; }
  .pick.on { border-color: var(--primary); background: var(--row); }
  .pick input, .mode input { margin-top: 3px; accent-color: var(--primary); flex: none; }
  .name { display: flex; flex-wrap: wrap; align-items: center; gap: var(--s2); }
  .chip.sm { font-size: var(--fs-xs); padding: 0 var(--s2); }
  .note { display: flex; gap: var(--s2); align-items: flex-start; color: var(--muted); font-size: var(--fs-xs); }
  .note :global(.i) { flex: none; margin-top: 2px; color: var(--warn); }
  .bad-text { color: var(--bad); font-size: var(--fs-xs); }
  .row :global(.i.failed) { color: var(--bad); margin-top: 3px; flex: none; }
  .row :global(.i.created), .row :global(.i.updated) { color: var(--good, var(--primary)); margin-top: 3px; flex: none; }
  .row :global(.i.skipped) { color: var(--muted); margin-top: 3px; flex: none; }
  .modes { display: grid; grid-template-columns: 1fr 1fr; gap: var(--s2); }
  .modes .caps { grid-column: 1 / -1; }
  .mode { display: flex; gap: var(--s2); align-items: flex-start; padding: var(--s3); border: 1px solid var(--line-soft); border-radius: var(--r-row); cursor: pointer; font-size: var(--fs-sm); }
  .mode.on { border-color: var(--primary); background: var(--row); }
  .mode .od-field span:last-child { font-size: var(--fs-xs); color: var(--muted); }
  .toggle { display: flex; gap: var(--s3); align-items: flex-start; font-size: var(--fs-sm); }
  .toggle .od-field span:last-child { font-size: var(--fs-xs); }
  @media (max-width: 559px) { .modes { grid-template-columns: minmax(0, 1fr); } }
</style>
