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

  let { value, save, admin = false, compact = false, myIP = '' } = $props()

  const clone = (v) => ({
    defaultIdent: v.defaultIdent || '', quitMsg: v.quitMsg || '', bindHost: v.bindHost || '', denySetBindHost: !!v.denySetBindHost,
    playbackLines: String(v.playbackLines ?? 50), autoClearChanBuffer: !!v.autoClearChanBuffer, autoClearQueryBuffer: !!v.autoClearQueryBuffer,
    prependTimestamp: !!v.prependTimestamp, appendTimestamp: !!v.appendTimestamp, timestampFormat: v.timestampFormat || '[%H:%M:%S]', timezone: v.timezone || '',
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
    if (!/^[0-9a-fA-F.:*?\[\]\/-]+$/.test(h)) { errs.newHost = 'Use an IP, a range like 10.0.0.0/8, or a wildcard like 192.168.*.'; return }
    errs.newHost = ''
    if (!f.allowedHosts.includes(h)) f.allowedHosts = [...f.allowedHosts, h]
    newHost = ''
  }

  async function submit(e) {
    e.preventDefault()
    formErr = ''
    const e2 = {}
    if (!/^\d+$/.test(f.playbackLines) || +f.playbackLines > 500) e2.playbackLines = 'A whole number, 0–500.'
    if (/[\s@!:]/.test(f.defaultIdent)) e2.defaultIdent = 'The ident can’t contain spaces, @, ! or :.'
    if (!f.timestampFormat.trim()) e2.timestampFormat = 'Give a format, e.g. [%H:%M:%S].'
    if (f.timezone && zones.length && !zones.includes(f.timezone) && f.timezone !== 'UTC') e2.timezone = 'Pick a time zone from the list, e.g. Europe/Berlin.'
    f.ctcp.forEach((c, i) => { if (c.k && !/^[A-Za-z0-9]{1,20}$/.test(c.k)) e2[`ctcp-${i}`] = 'One word, e.g. VERSION.' })
    errs = e2
    if (Object.keys(e2).length) return
    const settings = {
      defaultIdent: f.defaultIdent.trim(), quitMsg: f.quitMsg.trim(),
      playbackLines: +f.playbackLines, autoClearChanBuffer: f.autoClearChanBuffer, autoClearQueryBuffer: f.autoClearQueryBuffer,
      prependTimestamp: f.prependTimestamp, appendTimestamp: f.appendTimestamp, timestampFormat: f.timestampFormat.trim(), timezone: f.timezone.trim(),
      ctcpReplies: Object.fromEntries(f.ctcp.filter((c) => c.k.trim()).map((c) => [c.k.trim().toUpperCase(), c.r])),
      allowedHosts: f.allowedHosts,
    }
    if (admin || !binds.locked) settings.bindHost = f.bindHost.trim()
    busy = true
    try {
      const saved = await save(settings, f.denySetBindHost)
      f = clone(saved)
      toast('Bouncer settings saved')
    } catch (err) { formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' } finally { busy = false }
  }
</script>

<form class="form bouncer" class:compact novalidate onsubmit={submit} aria-label="Bouncer settings">
  {#if formErr}<div class="callout bad" role="alert"><Icon name="alert" /><span>{formErr}</span></div>{/if}

  <div class="group">
    <div class="od-stack head"><h3>Network defaults</h3><p class="desc">Used by networks that leave these empty.</p></div>
    <div class="form-grid">
      <Field id="b-ident" label="Ident" help="The user part of nick!ident@host. Empty = the nick." err={errs.defaultIdent}><input class="input mono" id="b-ident" spellcheck="false" autocomplete="off" bind:value={f.defaultIdent} aria-invalid={!!errs.defaultIdent} /></Field>
      <Field id="b-quit" label="Quit message" help="Sent when a network is switched off."><input class="input" id="b-quit" bind:value={f.quitMsg} /></Field>
      <Field id="b-bind" label="Bind host" span help={!admin && binds.locked ? 'Set by your admin.' : admin ? 'Any local address or host name on this server.' : binds.hosts.length ? 'The local address your networks connect from.' : 'This bouncer doesn’t offer a choice of address.'}>
        {#if admin}
          <input class="input mono" id="b-bind" placeholder="server default" bind:value={f.bindHost} />
        {:else}
          <Select id="b-bind" bind:value={f.bindHost} disabled={binds.locked || !binds.hosts.length} options={[
            { value: '', label: 'Server default' },
            ...binds.hosts.map((h) => ({ value: h, label: h })),
            ...(f.bindHost && !binds.hosts.includes(f.bindHost) ? [{ value: f.bindHost, label: f.bindHost }] : []),
          ]} />
        {/if}
      </Field>
      {#if admin}
        <div class="check-row span-2"><span class="od-field"><span id="b-deny-l">Lock the bind host</span><span>The user can’t change it (ZNC’s DenySetBindHost).</span></span><Switch checked={f.denySetBindHost} labelledby="b-deny-l" onchange={(v) => (f.denySetBindHost = v)} /></div>
      {/if}
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>Playback</h3><p class="desc">What an IRC client gets when it attaches. The web chat always shows full history.</p></div>
    <div class="form-grid">
      <Field id="b-lines" label="Lines per buffer" help="0 sends no playback." err={errs.playbackLines}><input class="input mono" id="b-lines" inputmode="numeric" bind:value={f.playbackLines} aria-invalid={!!errs.playbackLines} /></Field>
      <span></span>
      <div class="check-row span-2"><span class="od-field"><span id="b-acc-l">Only new channel lines</span><span>Channels play back what arrived since you were last attached, not the last lines every time.</span></span><Switch checked={f.autoClearChanBuffer} labelledby="b-acc-l" onchange={(v) => (f.autoClearChanBuffer = v)} /></div>
      <div class="check-row span-2"><span class="od-field"><span id="b-acq-l">Only new private messages</span><span>The same for private messages (ZNC’s default).</span></span><Switch checked={f.autoClearQueryBuffer} labelledby="b-acq-l" onchange={(v) => (f.autoClearQueryBuffer = v)} /></div>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>Timestamps</h3><p class="desc">For clients that don’t support server-time, played-back lines carry the time in the text.</p></div>
    <div class="form-grid">
      <div class="check-row"><span class="od-field"><span id="b-pre-l">Before the text</span><span>{preview} hello</span></span><Switch checked={f.prependTimestamp} labelledby="b-pre-l" onchange={(v) => (f.prependTimestamp = v)} /></div>
      <div class="check-row"><span class="od-field"><span id="b-app-l">After the text</span><span>hello {preview}</span></span><Switch checked={f.appendTimestamp} labelledby="b-app-l" onchange={(v) => (f.appendTimestamp = v)} /></div>
      <Field id="b-tsf" label="Format" help="strftime: %H:%M:%S, %Y-%m-%d, %a…" err={errs.timestampFormat}><input class="input mono" id="b-tsf" spellcheck="false" bind:value={f.timestampFormat} aria-invalid={!!errs.timestampFormat} /></Field>
      <Field id="b-tz" label="Time zone" help="Empty uses the server’s." err={errs.timezone}>
        <input class="input mono" id="b-tz" list="b-tz-list" placeholder="server time" spellcheck="false" bind:value={f.timezone} aria-invalid={!!errs.timezone} />
        <datalist id="b-tz-list">{#each zones as z}<option value={z}></option>{/each}</datalist>
      </Field>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>CTCP replies</h3><p class="desc">Answers evrd gives for you on every network. An empty reply blocks that request. With no client attached, evrd answers VERSION, PING and TIME itself.</p></div>
    <div class="od-stack" style="--od-gap:8px">
      {#each f.ctcp as c, i}
        <div class="ctcp-row">
          <Field id="ctcp-k-{i}" label="Request" err={errs[`ctcp-${i}`]}><input class="input mono" id="ctcp-k-{i}" placeholder="VERSION" spellcheck="false" bind:value={c.k} /></Field>
          <Field id="ctcp-r-{i}" label="Reply"><input class="input" id="ctcp-r-{i}" placeholder="(blocked)" bind:value={c.r} /></Field>
          <button class="icon-btn" type="button" aria-label="Remove CTCP reply {c.k || i + 1}" onclick={() => (f.ctcp = f.ctcp.filter((x) => x !== c))}><Icon name="trash" /></button>
        </div>
      {/each}
      <div><button class="btn btn-sm" type="button" onclick={() => (f.ctcp = [...f.ctcp, { k: '', r: '' }])}><Icon name="plus" />Add a reply</button></div>
    </div>
  </div>

  <div class="group">
    <div class="od-stack head"><h3>Allowed addresses</h3><p class="desc">Only these may sign in — web and IRC. Empty allows anywhere.{myIP ? ` You’re on ${myIP}.` : ''}</p></div>
    <div class="od-stack" style="--od-gap:8px">
      {#each f.allowedHosts as h}
        <div class="row host-row"><span class="mono">{h}</span><button class="btn btn-ghost btn-sm" type="button" aria-label="Remove {h}" onclick={() => (f.allowedHosts = f.allowedHosts.filter((x) => x !== h))}>Remove</button></div>
      {/each}
      <Field id="b-host" label="Add an address" help="An IP, a range (10.0.0.0/8) or a wildcard (192.168.*)." err={errs.newHost}>
        <div class="input-group">
          <input class="input mono" id="b-host" spellcheck="false" autocomplete="off" placeholder={myIP || '203.0.113.7'} bind:value={newHost} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addHost() } }} />
          <button class="btn" type="button" onclick={addHost}><Icon name="plus" />Add</button>
        </div>
      </Field>
    </div>
  </div>

  <div class="form-foot"><button class="btn btn-primary" type="submit" disabled={busy}>{#if busy}<span class="spinner" aria-hidden="true"></span>Saving…{:else}<Icon name="check" />Save bouncer settings{/if}</button></div>
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
