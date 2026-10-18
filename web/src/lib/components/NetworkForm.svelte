<script>
  // Add-a-network form, or edit one when `initial` is given. onsave(net) does
  // the request and throws on failure; the error is shown above the form.
  // When editing, net.password is undefined to keep the saved one, "" to
  // clear it.
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import ErrorSummary from './ErrorSummary.svelte'
  import { session } from '$lib/session.svelte.js'
  import { hostError, portError, nickError, channelsError, onBlurCheck } from '$lib/validate.js'

  let { onsave, oncancel = null, initial = null } = $props()
  const editing = !!initial

  const blank = () => initial
    ? { ...initial, password: '', channels: initial.channels.join(' ') }
    : { name: '', host: '', port: 6697, tls: true, nick: '', username: '', realname: '', password: '', channels: '' }
  let net = $state(blank())
  let clearPassword = $state(false)
  let errs = $state({})
  let summary = $state([])
  let formErr = $state('')
  let busy = $state(false)
  let showMore = $state(editing)

  const chans = () => net.channels.split(/[\s,]+/).map((c) => c.trim()).filter(Boolean)
  const checks = {
    'n-name': () => (!net.name.trim() ? 'Give the network a name, e.g. libera.' : /[\s/:]/.test(net.name.trim()) ? 'No spaces, / or : — the name goes in your IRC server password.' : ''),
    'n-host': () => hostError(net.host.trim()),
    'n-port': () => portError(net.port),
    'n-nick': () => nickError(net.nick.trim(), { optional: !editing && !!session.me?.defaultNick }),
    'n-chan': () => channelsError(chans()),
  }
  const check = (id) => (errs[id] = checks[id]())

  // TLS and port go together: flipping one moves the other off the default.
  function setTLS(on) {
    net.tls = on
    if (on && net.port === 6667) net.port = 6697
    if (!on && net.port === 6697) net.port = 6667
  }

  async function submit(e) {
    e.preventDefault()
    formErr = ''
    for (const id in checks) check(id)
    summary = Object.entries(errs).filter(([, m]) => m)
    if (summary.length) { document.getElementById(summary[0][0])?.focus(); return }
    busy = true
    try {
      const out = { ...net, name: net.name.trim(), host: net.host.trim(), nick: net.nick.trim(), port: Number(net.port), channels: chans() }
      if (editing) out.password = clearPassword ? '' : net.password || undefined
      await onsave(out)
      if (!editing) { net = blank(); errs = {}; showMore = false }
    } catch (err) {
      formErr = err.message.charAt(0).toUpperCase() + err.message.slice(1)
    } finally { busy = false }
  }
</script>

<form class="form" novalidate onsubmit={submit} aria-label={editing ? `Edit ${initial.name}` : 'Add a network'}>
  <ErrorSummary errs={summary} />
  {#if formErr}<div class="callout err" role="alert"><Icon name="alert" /><div class="body"><p>{formErr}</p></div></div>{/if}
  <div class="nf-grid">
    <Field id="n-name" label="Network name" required err={errs['n-name']}>
      <input id="n-name" class="input" placeholder="libera" bind:value={net.name} aria-invalid={errs['n-name'] ? 'true' : undefined} aria-describedby="n-name-err" onblur={onBlurCheck(() => net.name && check('n-name'))} />
    </Field>
    <Field id="n-host" label="Server" required err={errs['n-host']}>
      <input id="n-host" class="input" placeholder="irc.libera.chat" autocapitalize="none" spellcheck="false" bind:value={net.host} aria-invalid={errs['n-host'] ? 'true' : undefined} aria-describedby="n-host-err" onblur={onBlurCheck(() => net.host && check('n-host'))} />
    </Field>
    <Field id="n-port" label="Port" required err={errs['n-port']}>
      <div class="port-row">
        <input id="n-port" class="input" type="number" inputmode="numeric" min="1" max="65535" bind:value={net.port} aria-invalid={errs['n-port'] ? 'true' : undefined} aria-describedby="n-port-err" />
        <label class="check"><input type="checkbox" checked={net.tls} onchange={(e) => setTLS(e.currentTarget.checked)} /> TLS</label>
      </div>
    </Field>
    <Field id="n-nick" label="Nick" required={editing || !session.me?.defaultNick} help={!editing && session.me?.defaultNick ? `Empty uses your default, ${session.me.defaultNick}.` : ''} err={errs['n-nick']}>
      <input id="n-nick" class="input" placeholder={session.me?.defaultNick || 'yournick'} autocapitalize="none" spellcheck="false" bind:value={net.nick} aria-invalid={errs['n-nick'] ? 'true' : undefined} aria-describedby="n-nick-help n-nick-err" onblur={onBlurCheck(() => net.nick && check('n-nick'))} />
    </Field>
    <div class="span-2">
      <Field id="n-chan" label="Channels" help={editing ? 'Joined on connect. /join and /part update this list too.' : 'Joined on connect. Separate with spaces or commas.'} err={errs['n-chan']}>
        <input id="n-chan" class="input" placeholder="#ircforever #linux" autocapitalize="none" spellcheck="false" bind:value={net.channels} aria-invalid={errs['n-chan'] ? 'true' : undefined} aria-describedby="n-chan-help n-chan-err" onblur={onBlurCheck(() => net.channels && check('n-chan'))} />
      </Field>
    </div>
    {#if showMore}
      <Field id="n-user" label="Username" help="Ident. Empty uses your nick.">
        <input id="n-user" class="input" autocapitalize="none" spellcheck="false" bind:value={net.username} aria-describedby="n-user-help" />
      </Field>
      <Field id="n-real" label="Real name" help={session.me?.defaultRealname ? `Empty uses ${session.me.defaultRealname}.` : 'Shown in /whois.'}>
        <input id="n-real" class="input" bind:value={net.realname} aria-describedby="n-real-help" />
      </Field>
      <div class="span-2">
        <Field id="n-pass" label="Password" help={editing && initial.hasPassword ? 'A password is saved. Type a new one to replace it.' : 'Your NickServ / SASL password, or the server password. Sent with SASL PLAIN when the server supports it.'}>
          <input id="n-pass" class="input" type="password" autocomplete="off" placeholder={editing && initial.hasPassword ? '•••••••• (unchanged)' : ''} disabled={clearPassword} bind:value={net.password} aria-describedby="n-pass-help" />
        </Field>
        {#if editing && initial.hasPassword}
          <label class="check"><input type="checkbox" bind:checked={clearPassword} /> Remove the saved password</label>
        {/if}
      </div>
    {/if}
  </div>
  <div class="od-cluster">
    <button class="btn btn-primary" type="submit" disabled={busy}>
      {#if editing}
        {#if busy}<span class="spin" aria-hidden="true"></span>Saving…{:else}<Icon name="save" />Save changes{/if}
      {:else}
        {#if busy}<span class="spin" aria-hidden="true"></span>Adding…{:else}<Icon name="plus" />Add network{/if}
      {/if}
    </button>
    {#if !showMore}<button class="btn btn-ghost" type="button" onclick={() => (showMore = true)}><Icon name="sliders" />Identity &amp; password</button>{/if}
    {#if oncancel}<button class="btn btn-ghost" type="button" onclick={oncancel}>Cancel</button>{/if}
  </div>
</form>

<style>
  .nf-grid { display: grid; gap: var(--s4); grid-template-columns: minmax(0, 1fr); }
  @media (min-width: 768px) {
    .nf-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .span-2 { grid-column: 1 / -1; }
  }
  .port-row { display: flex; gap: var(--s3); align-items: center; }
  .port-row .input { flex: 1; min-width: 0; }
  .check { display: inline-flex; align-items: center; gap: var(--s2); font-size: var(--fs-sm); font-weight: 500; white-space: nowrap; cursor: pointer; min-height: 44px; }
  .check input { width: 18px; height: 18px; accent-color: var(--primary); }
</style>
