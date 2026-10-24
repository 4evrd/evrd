<script>
  // Bring a whole ZNC over: each of a config's users goes to an evrd
  // account (made when it doesn't exist, keeping the ZNC password; with an
  // email, claimable by the owner's first SSO sign-in), then its networks are
  // imported there, one account at a time on the node that serves it.
  import Icon from '$lib/components/Icon.svelte'
  import Field from '$lib/components/Field.svelte'
  import Switch from '$lib/components/Switch.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  const isAdmin = $derived(session.me?.role === 'admin')

  let input = $state()
  let pasted = $state('')
  let text = $state('')
  let filename = $state('')
  let rows = $state(null) // the preview's users, with the page's choices
  let err = $state('')
  let busy = $state(false)
  let existing = $state('skip')
  let connect = $state(false)
  let progress = $state('')
  let results = $state(null)

  const chosen = $derived((rows || []).filter((r) => r.include && !r.accountError))
  const netCount = $derived(chosen.reduce((n, r) => n + r.networks.filter((x) => r.picked[x.name]).length, 0))
  const anyExisting = $derived(chosen.some((r) => r.networks.some((x) => x.exists && r.picked[x.name])))

  // rows from a preview, keeping what was already chosen for each ZNC user
  function fromPreview(users) {
    const before = Object.fromEntries((rows || []).map((r) => [r.name, r]))
    return users.map((u) => {
      const b = before[u.name]
      const picked = Object.fromEntries(u.networks.map((n) => [n.name, b?.picked[n.name] ?? !n.exists]))
      return { ...u, include: b?.include ?? true, email: b?.email ?? '', input: u.account, picked }
    })
  }

  async function read(body, name) {
    busy = true
    err = ''
    results = null
    try {
      const out = await api.admin.zncPreview(body, {})
      text = body
      filename = name
      rows = null
      rows = fromPreview(out.users)
    } catch (e) { err = e.message } finally { busy = false }
  }

  async function chose(e) {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ''
    if (f) read(await f.text(), f.name)
  }

  // an account renamed: look again at whether it exists, and its networks
  async function remap() {
    try {
      const out = await api.admin.zncPreview(text, Object.fromEntries(rows.map((r) => [r.name, r.input])))
      rows = fromPreview(out.users)
    } catch (e) { err = e.message }
  }

  function reset() {
    rows = results = null
    text = filename = err = progress = ''
  }

  async function go() {
    busy = true
    err = ''
    const done = []
    try {
      const made = await api.admin.zncAccounts(text, chosen.map((r) => ({ znc: r.name, account: r.account, email: r.exists ? '' : r.email.trim() })))
      for (const [i, a] of made.accounts.entries()) {
        const r = chosen.find((x) => x.name === a.znc)
        const res = { ...a, networks: [] }
        done.push(res)
        if (a.error || !a.id) continue
        const nets = r.networks.filter((n) => r.picked[n.name]).map((n) => n.name)
        if (!nets.length) continue
        progress = t('zncMove.progress', { name: a.account, n: i + 1, count: made.accounts.length })
        try {
          res.networks = (await api.admin.importNetworks(a.id, { text, user: a.znc, networks: nets, existing, connect })).results
        } catch (e) { res.error = e.message }
      }
      results = done
    } catch (e) { err = e.message } finally { busy = false; progress = '' }
  }
</script>

<svelte:head><title>{t('zncMove.title')} · evrd</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b></span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/admin/users"><Icon name="chevron-left" class="i-sm" />{t('adminUsers.heading')}</a>
      <h1>{t('zncMove.title')}</h1>
      <p>{t('zncMove.intro')}</p>
    </div>
  </div>

  <div class="zm enter">
    {#if !rows}
      <section class="card od-stack" data-testid="znc-pick">
        <input class="file" type="file" accept=".conf,.txt,text/plain" bind:this={input} onchange={chose} data-testid="znc-file" tabindex="-1" aria-hidden="true" />
        <div><button class="btn btn-primary" type="button" onclick={() => input.click()} disabled={busy}><Icon name="upload" />{t('zncMove.choose')}</button></div>
        <Field id="zm-paste" label={t('transfer.paste')}>
          <textarea class="textarea mono" id="zm-paste" rows="8" spellcheck="false" placeholder={'<User alice>\n  <Network libera>\n    Server = irc.libera.chat +6697\n…'} bind:value={pasted} data-testid="znc-paste"></textarea>
        </Field>
        <div><button class="btn" type="button" onclick={() => read(pasted, '')} disabled={busy || !pasted.trim()} data-testid="znc-read">{#if busy}<span class="spinner" aria-hidden="true"></span>{t('transfer.reading')}{:else}{t('transfer.read')}{/if}</button></div>
        <p class="help"><Icon name="lock" class="i-sm" /> {t('zncMove.passwords')}</p>
      </section>
    {:else if results}
      <section class="card od-stack" data-testid="znc-results">
        <h2>{t('transfer.done')}</h2>
        <ul class="users">
          {#each results as a (a.znc)}
            <li class="user" data-testid="znc-result">
              <div class="uhead">
                <Icon name={a.error ? 'alert' : 'check'} class="i-sm {a.error ? 'failed' : 'created'}" />
                <span class="od-field">
                  <span><span class="mono faint">{a.znc}</span> → {#if a.id}<a href="/admin/users/{a.id}"><b>{a.account}</b></a>{:else}<b>{a.account}</b>{/if}
                    · {a.error ? t('transfer.result.failed') : a.created ? t('zncMove.created') : t('zncMove.existed')}</span>
                  {#if a.error}<span class="bad-text">{a.error}</span>{/if}
                  {#if a.created}<span class="faint small">{[a.password ? t('zncMove.keptPassword') : t('zncMove.noPassword'), a.claimable ? t('zncMove.claimable') : ''].filter(Boolean).join(' · ')}</span>{/if}
                  {#if a.note}<span class="note"><Icon name="alert" class="i-sm" />{a.note}</span>{/if}
                </span>
              </div>
              {#if a.networks.length}
                <ul class="nets">
                  {#each a.networks as r (r.name)}
                    <li data-result={r.result}><b>{r.name}</b> · {t('transfer.result.' + r.result)}{#if r.error}<span class="bad-text"> · {r.error}</span>{/if}
                      {#each r.notes as note}<span class="note"><Icon name="alert" class="i-sm" />{note}</span>{/each}</li>
                  {/each}
                </ul>
              {/if}
            </li>
          {/each}
        </ul>
        <div class="od-cluster"><a class="btn btn-primary" href="/admin/users">{t('zncMove.toUsers')}</a><button class="btn btn-ghost" type="button" onclick={reset}>{t('transfer.another')}</button></div>
      </section>
    {:else}
      <section class="card od-stack" data-testid="znc-preview">
        <div class="src">
          <span class="chip"><Icon name="server" />{t('transfer.formatZnc')}</span>
          {#if filename}<span class="mono faint od-truncate">{filename}</span>{/if}
          <button class="btn btn-ghost btn-sm" type="button" onclick={reset}>{t('transfer.another')}</button>
        </div>
        {#if !rows.length}<p class="faint">{t('zncMove.noUsers')}</p>{/if}
        <ul class="users">
          {#each rows as r (r.name)}
            <li class="user" class:off={!r.include} data-testid="znc-user">
              <div class="uhead">
                <input type="checkbox" bind:checked={r.include} aria-label={t('zncMove.includeUser', { name: r.name })} />
                <span class="mono zname">{r.name}</span>
                <Icon name="chevron" class="i-sm faint" />
                <input class="input acct" aria-label={t('zncMove.account', { name: r.name })} bind:value={r.input} onchange={remap} spellcheck="false" autocomplete="off" data-testid="znc-account" />
                {#if r.accountError}<span class="bad-text acct-err">{r.accountError}</span>
                {:else if r.exists}<a class="chip sm" href="/admin/users/{r.accountID}">{t('zncMove.existing')}</a>
                {:else}<span class="chip ok sm">{t('zncMove.new')}</span>{/if}
              </div>
              {#if r.include && !r.accountError}
                {#if !r.exists}
                  <div class="newacct">
                    <span class="faint small"><Icon name="key" class="i-sm" /> {r.hasPassword ? t('zncMove.willKeepPassword') : t('zncMove.noZncPassword')}</span>
                    <input class="input" type="email" placeholder={t('zncMove.email')} aria-label={t('zncMove.emailFor', { name: r.account })} bind:value={r.email} autocomplete="off" data-testid="znc-email" />
                    <span class="faint small">{t('zncMove.emailHelp')}</span>
                  </div>
                {/if}
                <ul class="nets">
                  {#each r.networks as n (n.name)}
                    <li>
                      <label class="pick">
                        <input type="checkbox" bind:checked={r.picked[n.name]} />
                        <span class="od-field">
                          <span><b>{n.name}</b> <span class="mono faint">{[n.servers.join(', '), t('transfer.channels', { count: n.channels })].filter(Boolean).join(' · ')}</span>
                            {#if n.exists}<span class="chip sm">{t('transfer.exists')}</span>{/if}</span>
                          {#each n.notes as note}<span class="note"><Icon name="alert" class="i-sm" />{note}</span>{/each}
                        </span>
                      </label>
                    </li>
                  {/each}
                </ul>
              {/if}
            </li>
          {/each}
        </ul>

        {#if anyExisting}
          <div class="modes" role="radiogroup" aria-label={t('transfer.existing')}>
            <span class="caps">{t('transfer.existing')}</span>
            <label class="mode" class:on={existing === 'skip'}><input type="radio" name="zm-existing" value="skip" bind:group={existing} /><span class="od-field"><span>{t('transfer.skip')}</span><span>{t('transfer.skipHelp')}</span></span></label>
            <label class="mode" class:on={existing === 'merge'}><input type="radio" name="zm-existing" value="merge" bind:group={existing} /><span class="od-field"><span>{t('transfer.merge')}</span><span>{t('transfer.mergeHelp')}</span></span></label>
            <label class="mode" class:on={existing === 'update'}><input type="radio" name="zm-existing" value="update" bind:group={existing} /><span class="od-field"><span>{t('transfer.update')}</span><span>{t('transfer.updateHelp')}</span></span></label>
          </div>
        {/if}
        <div class="toggle">
          <Switch checked={connect} labelledby="zm-connect" onchange={(v) => (connect = v)} />
          <span class="od-field"><span id="zm-connect">{t('transfer.connect')}</span><span class="faint">{t('transfer.connectHelp')}</span></span>
        </div>
        {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{err}</span></div>{/if}
        <div class="od-cluster">
          <button class="btn btn-primary" type="button" onclick={go} disabled={busy || !chosen.length} data-testid="znc-go">{#if busy}<span class="spinner" aria-hidden="true"></span>{progress || t('transfer.importing')}{:else}<Icon name="check" />{t('zncMove.go', { count: chosen.length, networks: netCount })}{/if}</button>
        </div>
      </section>
    {/if}
    {#if err && !rows}<div class="callout bad" role="alert" data-testid="znc-error"><Icon name="alert" /><span>{err}</span></div>{/if}
  </div>
{/if}

<style>
  .zm { display: grid; gap: var(--s4); max-width: 860px; margin-top: var(--s5); }
  .zm .od-stack { --od-gap: var(--s4); }
  .back { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: var(--fs-sm); }
  .file { display: none; }
  .help { display: flex; gap: var(--s2); align-items: flex-start; color: var(--muted); font-size: var(--fs-xs); }
  .help :global(.i) { flex: none; margin-top: 2px; }
  .src { display: flex; flex-wrap: wrap; align-items: center; gap: var(--s3); }
  .src .mono { min-width: 0; flex: 1; font-size: var(--fs-xs); }
  .users { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--s3); margin: 0; padding: 0; list-style: none; }
  .user { display: grid; grid-template-columns: minmax(0, 1fr); min-width: 0; gap: var(--s3); padding: var(--s3) var(--s4); border: 1px solid var(--line-soft); border-radius: var(--r-row); font-size: var(--fs-sm); }
  .user.off { opacity: 0.6; }
  .uhead { display: flex; flex-wrap: wrap; align-items: center; gap: var(--s2) var(--s3); }
  .uhead .od-field { --od-gap: 4px; min-width: 0; }
  .zname { font-weight: 600; }
  .acct { flex: 0 1 14rem; min-width: 8rem; height: 34px; }
  .newacct { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--s2) var(--s3); align-items: center; }
  .newacct > span:first-child { grid-column: 1 / -1; display: flex; gap: var(--s2); align-items: center; }
  .newacct .input { height: 34px; }
  .nets { display: grid; gap: var(--s2); margin: 0; padding: 0 0 0 var(--s5); list-style: none; }
  .nets li { display: grid; gap: 4px; }
  .pick { display: flex; gap: var(--s2); align-items: flex-start; cursor: pointer; }
  .pick .od-field { --od-gap: 4px; min-width: 0; }
  .pick .mono { overflow-wrap: anywhere; }
  .pick .mono, .faint.small, .small { font-size: var(--fs-xs); }
  input[type='checkbox'], .mode input { accent-color: var(--primary); flex: none; margin-top: 3px; }
  .chip.sm { font-size: var(--fs-xs); padding: 0 var(--s2); }
  .note { display: flex; gap: var(--s2); align-items: flex-start; color: var(--muted); font-size: var(--fs-xs); }
  .note :global(.i) { flex: none; margin-top: 2px; color: var(--warn); }
  .bad-text { color: var(--bad); font-size: var(--fs-xs); }
  .acct-err { flex: 1 1 12rem; min-width: 0; }
  .uhead :global(.i.failed) { color: var(--bad); }
  .uhead :global(.i.created) { color: var(--primary); }
  .modes { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--s2); }
  .modes .caps { grid-column: 1 / -1; }
  .mode { display: flex; gap: var(--s2); align-items: flex-start; padding: var(--s3); border: 1px solid var(--line-soft); border-radius: var(--r-row); cursor: pointer; font-size: var(--fs-sm); }
  .mode.on { border-color: var(--primary); background: var(--row); }
  .mode .od-field span:last-child { font-size: var(--fs-xs); color: var(--muted); }
  .toggle { display: flex; gap: var(--s3); align-items: flex-start; font-size: var(--fs-sm); }
  .toggle .od-field span:last-child { font-size: var(--fs-xs); }
  @media (max-width: 719px) { .modes, .newacct { grid-template-columns: minmax(0, 1fr); } .nets { padding-left: 0; } }
</style>
