<script>
  // A user's ZNC-style bouncer settings: network defaults, playback,
  // timestamps, CTCP replies and allowed hosts. Used on the account page and
  // (admin = true) in the admin's user drawer.
  //   value  the user's settings (from the API)
  //   save   async (settings) => saved settings
  import { untrack } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Select from './Select.svelte'
  import Switch from './Switch.svelte'
  import { api } from '$lib/api.js'
  import { toast } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let { value, save, admin = false, compact = false, myIP = '' } = $props()

  const clone = (v) => ({
    defaultIdent: v.defaultIdent || '', quitMsg: v.quitMsg || '', bindHost: v.bindHost || '', denySetBindHost: !!v.denySetBindHost,
    playbackLines: String(v.playbackLines ?? 50), autoClearChanBuffer: !!v.autoClearChanBuffer, autoClearQueryBuffer: !!v.autoClearQueryBuffer,
    prependTimestamp: !!v.prependTimestamp, appendTimestamp: !!v.appendTimestamp, timestampFormat: v.timestampFormat || '[%H:%M:%S]', timezone: v.timezone || '',
    quietRejoin: !!v.quietRejoin,
    ctcp: Object.entries(v.ctcpReplies || {}).map(([k, r]) => ({ k, r })),
    allowedHosts: [...(v.allowedHosts || [])],
  })
  let f = $state(clone(untrack(() => value))) // the form starts from the saved settings
  let errs = $state({})
  let formErr = $state('')
  let busy = $state(false)
  let binds = $state({ hosts: [], locked: false, any: false })
  let newHost = $state('')
  $effect(() => { if (admin) binds = { hosts: [], locked: false, any: true }; else api.bindHosts().then((b) => (binds = { ...b, hosts: b.hosts || [] })).catch(() => {}) })
  const zones = (() => { try { return Intl.supportedValuesOf('timeZone') } catch { return [] } })()
  const pad = (n) => String(n).padStart(2, '0')
  // a live preview of the timestamp format
  const preview = $derived.by(() => {
    const d = new Date()
    const map = { H: pad(d.getHours()), M: pad(d.getMinutes()), S: pad(d.getSeconds()), Y: d.getFullYear(), y: pad(d.getFullYear() % 100), m: pad(d.getMonth() + 1), d: pad(d.getDate()), '%': '%' }
    return f.timestampFormat.replace(/%(.)/g, (m, c) => map[c] ?? m)
  })

  function addHost() {
    const h = newHost.trim()
    if (!h) return
    if (!/^[0-9a-fA-F.:*?\[\]\/-]+$/.test(h)) { errs.newHost = t('account.bouncer.hostInvalid'); return }
    errs.newHost = ''
    if (!f.allowedHosts.includes(h)) f.allowedHosts = [...f.allowedHosts, h]
    newHost = ''
  }

  async function submit(e) {
    e.preventDefault()
    formErr = ''
    const e2 = {}
    if (!/^\d+$/.test(f.playbackLines) || +f.playbackLines > 500) e2.playbackLines = t('account.bouncer.linesInvalid')
    if (/[\s@!:]/.test(f.defaultIdent)) e2.defaultIdent = t('account.bouncer.identInvalid')
    if (!f.timestampFormat.trim()) e2.timestampFormat = t('account.bouncer.formatMissing')
    if (f.timezone && zones.length && !zones.includes(f.timezone) && f.timezone !== 'UTC') e2.timezone = t('account.bouncer.zoneInvalid')
    f.ctcp.forEach((c, i) => { if (c.k && !/^[A-Za-z0-9]{1,20}$/.test(c.k)) e2[`ctcp-${i}`] = t('account.bouncer.ctcpInvalid') })
    errs = e2
    if (Object.keys(e2).length) return
    const settings = {
      defaultIdent: f.defaultIdent.trim(), quitMsg: f.quitMsg.trim(),
      playbackLines: +f.playbackLines, autoClearChanBuffer: f.autoClearChanBuffer, autoClearQueryBuffer: f.autoClearQueryBuffer,
      prependTimestamp: f.prependTimestamp, appendTimestamp: f.appendTimestamp, timestampFormat: f.timestampFormat.trim(), timezone: f.timezone.trim(),
      quietRejoin: f.quietRejoin,
      ctcpReplies: Object.fromEntries(f.ctcp.filter((c) => c.k.trim()).map((c) => [c.k.trim().toUpperCase(), c.r])),
      allowedHosts: f.allowedHosts,
    }
    if (admin || !binds.locked) settings.bindHost = f.bindHost.trim()
    busy = true
    try {
      const saved = await save(settings, f.denySetBindHost)
      f = clone(saved)
      toast(t('account.bouncer.saved'))
    } catch (err) { formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' } finally { busy = false }
  }
</script>

<form class="form bouncer" class:compact novalidate onsubmit={submit} aria-label={t('account.bouncer.label')}>
  {#if formErr}<div class="callout bad" role="alert"><Icon name="alert" /><span>{formErr}</span></div>{/if}

  <div class="group">
    <div class="od-stack head"><h3>{t('account.bouncer.netDefaults')}</h3><p class="desc">{t('account.bouncer.netDefaultsHelp')}</p></div>
    <div class="form-grid">
      <Field id="b-ident" label={t('account.bouncer.ident')} help={t('account.bouncer.identHelp')} err={errs.defaultIdent}><input class="input mono" id="b-ident" spellcheck="false" autocomplete="off" bind:value={f.defaultIdent} aria-invalid={!!errs.defaultIdent} /></Field>
      <Field id="b-quit" label={t('account.bouncer.quitMsg')} help={t('account.bouncer.quitMsgHelp')}><input class="input" id="b-quit" bind:value={f.quitMsg} /></Field>
      <Field id="b-bind" label={t('account.bouncer.bindHost')} span help={!admin && binds.locked ? t('account.bouncer.bindLocked') : admin ? t('account.bouncer.bindAdmin') : binds.hosts.length ? t('account.bouncer.bindHelp') : t('account.bouncer.bindNone')}>
        {#if admin}
          <input class="input mono" id="b-bind" placeholder={t('account.bouncer.serverDefaultPh')} bind:value={f.bindHost} />
        {:else}
          <Select id="b-bind" bind:value={f.bindHost} disabled={binds.locked || !binds.hosts.length} options={[
            { value: '', label: t('account.bouncer.serverDefault') },
            ...binds.hosts.map((h) => ({ value: h, label: h })),
            ...(f.bindHost && !binds.hosts.includes(f.bindHost) ? [{ value: f.bindHost, label: f.bindHost }] : []),
          ]} />
        {/if}
      </Field>
      <div class="check-row span-2"><span class="od-field"><span id="b-quiet-l">{t('account.bouncer.quietRejoin')}</span><span>{t('account.bouncer.quietRejoinHelp')}</span></span><Switch checked={f.quietRejoin} labelledby="b-quiet-l" onchange={(v) => (f.quietRejoin = v)} /></div>
      {#if admin}
        <div class="check-row span-2"><span class="od-field"><span id="b-deny-l">{t('account.bouncer.lockBind')}</span><span>{t('account.bouncer.lockBindHelp')}</span></span><Switch checked={f.denySetBindHost} labelledby="b-deny-l" onchange={(v) => (f.denySetBindHost = v)} /></div>
      {/if}
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>{t('account.bouncer.playback')}</h3><p class="desc">{t('account.bouncer.playbackHelp')}</p></div>
    <div class="form-grid">
      <Field id="b-lines" label={t('account.bouncer.lines')} help={t('account.bouncer.linesHelp')} err={errs.playbackLines}><input class="input mono" id="b-lines" inputmode="numeric" bind:value={f.playbackLines} aria-invalid={!!errs.playbackLines} /></Field>
      <span></span>
      <div class="check-row span-2"><span class="od-field"><span id="b-acc-l">{t('account.bouncer.newChan')}</span><span>{t('account.bouncer.newChanHelp')}</span></span><Switch checked={f.autoClearChanBuffer} labelledby="b-acc-l" onchange={(v) => (f.autoClearChanBuffer = v)} /></div>
      <div class="check-row span-2"><span class="od-field"><span id="b-acq-l">{t('account.bouncer.newQuery')}</span><span>{t('account.bouncer.newQueryHelp')}</span></span><Switch checked={f.autoClearQueryBuffer} labelledby="b-acq-l" onchange={(v) => (f.autoClearQueryBuffer = v)} /></div>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>{t('account.bouncer.timestamps')}</h3><p class="desc">{t('account.bouncer.timestampsHelp')}</p></div>
    <div class="form-grid">
      <div class="check-row"><span class="od-field"><span id="b-pre-l">{t('account.bouncer.before')}</span><span>{t('account.bouncer.beforeSample', { time: preview })}</span></span><Switch checked={f.prependTimestamp} labelledby="b-pre-l" onchange={(v) => (f.prependTimestamp = v)} /></div>
      <div class="check-row"><span class="od-field"><span id="b-app-l">{t('account.bouncer.after')}</span><span>{t('account.bouncer.afterSample', { time: preview })}</span></span><Switch checked={f.appendTimestamp} labelledby="b-app-l" onchange={(v) => (f.appendTimestamp = v)} /></div>
      <Field id="b-tsf" label={t('account.bouncer.format')} help="strftime: %H:%M:%S, %Y-%m-%d, %a…" err={errs.timestampFormat}><input class="input mono" id="b-tsf" spellcheck="false" bind:value={f.timestampFormat} aria-invalid={!!errs.timestampFormat} /></Field>
      <Field id="b-tz" label={t('account.bouncer.zone')} help={t('account.bouncer.zoneHelp')} err={errs.timezone}>
        <input class="input mono" id="b-tz" list="b-tz-list" placeholder={t('account.bouncer.serverTime')} spellcheck="false" bind:value={f.timezone} aria-invalid={!!errs.timezone} />
        <datalist id="b-tz-list">{#each zones as z}<option value={z}></option>{/each}</datalist>
      </Field>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>{t('account.bouncer.ctcp')}</h3><p class="desc">{t('account.bouncer.ctcpHelp')}</p></div>
    <div class="od-stack" style="--od-gap:8px">
      {#each f.ctcp as c, i}
        <div class="ctcp-row">
          <Field id="ctcp-k-{i}" label={t('account.bouncer.request')} err={errs[`ctcp-${i}`]}><input class="input mono" id="ctcp-k-{i}" placeholder="VERSION" spellcheck="false" bind:value={c.k} /></Field>
          <Field id="ctcp-r-{i}" label={t('account.bouncer.reply')}><input class="input" id="ctcp-r-{i}" placeholder={t('account.bouncer.blocked')} bind:value={c.r} /></Field>
          <button class="icon-btn" type="button" aria-label={t('account.bouncer.removeCtcp', { name: c.k || i + 1 })} onclick={() => (f.ctcp = f.ctcp.filter((x) => x !== c))}><Icon name="trash" /></button>
        </div>
      {/each}
      <div><button class="btn btn-sm" type="button" onclick={() => (f.ctcp = [...f.ctcp, { k: '', r: '' }])}><Icon name="plus" />{t('account.bouncer.addReply')}</button></div>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>{t('account.bouncer.hosts')}</h3><p class="desc">{t('account.bouncer.hostsHelp')}{myIP ? ' ' + t('account.bouncer.youreOn', { ip: myIP }) : ''}</p></div>
    <div class="od-stack" style="--od-gap:8px">
      {#each f.allowedHosts as h}
        <div class="row host-row"><span class="mono">{h}</span><button class="btn btn-ghost btn-sm" type="button" aria-label={t('account.bouncer.removeHost', { host: h })} onclick={() => (f.allowedHosts = f.allowedHosts.filter((x) => x !== h))}>{t('account.bouncer.remove')}</button></div>
      {/each}
      <Field id="b-host" label={t('account.bouncer.addHost')} help={t('account.bouncer.addHostHelp')} err={errs.newHost}>
        <div class="input-group">
          <input class="input mono" id="b-host" spellcheck="false" autocomplete="off" placeholder={myIP || '203.0.113.7'} bind:value={newHost} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addHost() } }} />
          <button class="btn" type="button" onclick={addHost}><Icon name="plus" />{t('account.bouncer.add')}</button>
        </div>
      </Field>
    </div>
  </div>

  <div class="form-foot"><button class="btn btn-primary" type="submit" disabled={busy}>{#if busy}<span class="spinner" aria-hidden="true"></span>{t('account.saving')}{:else}<Icon name="check" />{t('account.bouncer.save')}{/if}</button></div>
</form>

<style>
  .bouncer { display: grid; gap: var(--s6); }
  .group { display: grid; grid-template-columns: minmax(0, 240px) minmax(0, 1fr); gap: var(--s7); }
  .group .head { --od-gap: 6px; }
  .group h3 { font-size: var(--fs-md); font-weight: 500; }
  .compact .group { grid-template-columns: minmax(0, 1fr); gap: var(--s3); }
  .ctcp-row { display: grid; grid-template-columns: 160px minmax(0, 1fr) auto; gap: var(--s3); align-items: end; }
  .ctcp-row .icon-btn { margin-bottom: 4px; }
  .host-row { --cols: minmax(0, 1fr) auto; }
  @media (max-width: 959px) { .group { grid-template-columns: minmax(0, 1fr); gap: var(--s3); } }
</style>
