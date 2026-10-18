<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Copy from '$lib/components/Copy.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import { api } from '$lib/api.js'
  import { session, refreshMe, ircPort } from '$lib/session.svelte.js'
  import { limit, usePct, level } from '$lib/format.js'

  const me = $derived(session.me)
  const port = $derived(ircPort())
  let nets = $state(null)
  let err = $state('')
  let timer

  async function refresh() {
    try { nets = await api.networks(); await refreshMe(); err = '' } catch (e) { err = e.message }
  }
  onMount(() => { refresh(); timer = setInterval(refresh, 5000) })
  onDestroy(() => clearInterval(timer))

  const connected = $derived(nets ? nets.filter((n) => n.status === 'connected').length : 0)
  const netPct = $derived(usePct(me.usage.networks, me.limits.maxNetworks))
  const cliPct = $derived(usePct(me.usage.clients, me.limits.maxClients))
</script>

<svelte:head><title>Overview · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Overview</h1>
    <p>Hi {me.username} — here’s your bouncer at a glance.</p>
  </div>
  <div class="od-cluster">
    <a class="btn" href="/clients"><Icon name="plug" />Connect a client</a>
    <a class="btn btn-primary" href="/networks#add"><Icon name="plus" />Add network</a>
  </div>
</div>

<div class="tiles">
  <a class="tile" href="/networks">
    <div class="tile-top"><span class="tile-label"><Icon name="globe" />Networks</span><span class="link-btn">Manage</span></div>
    <div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n" data-testid="tile-networks">{me.usage.networks}<span class="unit">/ {limit(me.limits.maxNetworks)}</span></span><span class="u">networks set up</span></div>
        <span class="tile-ic blue"><Icon name="server" /></span>
      </div>
      {#if me.limits.maxNetworks}<span class="ubar {level(netPct)}" role="img" aria-label="{Math.round(netPct)}% of your network limit"><span style="transform:scaleX({netPct / 100})"></span></span>{/if}
    </div>
  </a>
  <a class="tile" href="/networks">
    <div class="tile-top"><span class="tile-label"><Icon name="activity" />Connected</span><span class="link-btn">Status</span></div>
    <div class="tile-bottom">
      <div class="tile-num"><span class="n" data-testid="tile-connected">{nets ? connected : '–'}</span><span class="u">{nets && connected === nets.length && nets.length ? 'all online' : 'online right now'}</span></div>
      <span class="tile-ic {connected ? 'ok' : ''}"><Icon name="play" /></span>
    </div>
  </a>
  <a class="tile" href="/clients">
    <div class="tile-top"><span class="tile-label"><Icon name="plug" />IRC clients</span><span class="link-btn">Set up</span></div>
    <div>
      <div class="tile-bottom">
        <div class="tile-num"><span class="n" data-testid="tile-clients">{me.usage.clients}<span class="unit">/ {limit(me.limits.maxClients)}</span></span><span class="u">attached now</span></div>
        <span class="tile-ic"><Icon name="monitor" /></span>
      </div>
      {#if me.limits.maxClients}<span class="ubar mem {level(cliPct)}" role="img" aria-label="{Math.round(cliPct)}% of your client limit"><span style="transform:scaleX({cliPct / 100})"></span></span>{/if}
    </div>
  </a>
  <div class="tile">
    <div class="tile-top"><span class="tile-label"><Icon name="message" />Backlog</span></div>
    <div class="tile-bottom">
      <div class="tile-num"><span class="n word">{me.limits.backlogLines ? me.limits.backlogLines.toLocaleString('en') : 'Unlimited'}</span><span class="u">{me.limits.backlogLines ? 'lines kept per channel' : 'every line is kept'}</span></div>
      <span class="tile-ic warn"><Icon name="disk" /></span>
    </div>
  </div>
</div>

<div class="grid-2 main-side enter">
  <section class="card" aria-labelledby="nets-h">
    <div class="section-head">
      <h2 id="nets-h"><Icon name="globe" />Your networks</h2>
      <a class="link-btn" href="/networks">All networks</a>
    </div>
    <div class="rows">
      {#if err}
        <p class="desc">Couldn’t load your networks: {err}</p>
      {:else if !nets}
        <div class="skel"></div><div class="skel"></div>
      {:else if !nets.length}
        <div class="empty"><span class="ic"><Icon name="globe" /></span><h4>No networks yet</h4><p>Add the IRC networks you use — evrd connects to them and stays connected for you.</p><a class="btn btn-primary btn-sm" href="/networks#add"><Icon name="plus" />Add network</a></div>
      {:else}
        {#each nets.slice(0, 6) as n (n.id)}
          <div class="row" style="--cols:minmax(0,1fr) auto">
            <span class="main"><span class="name"><a href="/networks/{n.id}">{n.name}</a></span><span class="sub">{n.nick} · {n.host}:{n.port}{n.tls ? ' · TLS' : ''}</span></span>
            <span class="meta"><StatusChip status={n.status} /></span>
          </div>
        {/each}
      {/if}
    </div>
  </section>

  <section class="card frost connect" aria-labelledby="att-h">
    <h3 id="att-h"><Icon name="plug" />Attach a client</h3>
    <div class="kv-list">
      <div class="kv"><div class="text"><span class="k">Server</span><span class="v">{session.config.ircHost}</span></div><Copy text={session.config.ircHost} label="Copy server" /></div>
      <div class="kv"><div class="text"><span class="k">Port</span><span class="v">{port.port}{port.tls ? ' (TLS)' : ''}</span></div></div>
      <div class="kv"><div class="text"><span class="k">Server password</span><span class="v">{me.username}/<i>network</i>:<i>password</i></span></div></div>
    </div>
    {#if me.hasPassword}
      <p class="desc" style="font-size:14px">The password is your account password or an <a href="/clients">IRC client password</a>. Leave out <span class="code">/network</span> to attach to your first one.</p>
    {:else}
      <p class="desc" style="font-size:14px">You sign in with {session.config.ssoLabel}, so clients use an <a href="/clients">IRC client password</a> instead.</p>
    {/if}
  </section>
</div>
