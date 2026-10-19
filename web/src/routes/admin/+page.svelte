<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Topology from '$lib/components/Topology.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { ago, since, clock, date, limit, ratio, netColor, nodeBadge } from '$lib/format.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  let ov = $state(null)
  let err = $state('')
  let timer
  async function load() {
    try { ov = await api.admin.overview(); err = '' } catch (e) { err = e.message }
  }
  onMount(() => { if (!isAdmin) return; load(); timer = setInterval(load, 15000) })
  onDestroy(() => clearInterval(timer))

  const nodes = $derived((ov?.nodes || []).map((n, i) => ({ ...n, color: netColor(i) })))
  const now = () => Date.now() / 1000
  // Seconds the server's clock is behind (+) or ahead of this browser's:
  // heartbeats and start times are in server time.
  const skew = $derived(ov?.now ? now() - ov.now : 0)
  const nodeLive = (n) => now() - skew - n.heartbeatAt < 30
  const healthy = $derived(nodes.length > 0 && nodes.every(nodeLive))
  const placed = $derived((ov?.placement || []).filter((p) => p.node || p.status === 'suspended'))

  let filter = $state('all')
  const match = { all: () => true, online: (p) => p.status === 'online', issue: (p) => !!p.issue }
  const label = { all: 'Accounts', online: 'Online now', issue: 'Accounts with an issue' }
  const shown = $derived((ov?.placement || []).filter(match[filter]).map((p) => ({ ...p, key: p.id, source: p.node || '-', dashed: p.status !== 'online' })))
  const leftNodes = $derived(nodes.map((n) => ({ id: n.id, color: n.color })))

  const attnIcon = { network: ['alert', 'signal'], limit: ['sliders', 'warn'], suspended: ['ban', 'bad'], inactive: ['user', 'warn'] }
  const maxUsers = $derived(Math.max(1, ...(ov?.upstreams || []).map((u) => u.users)))
  const storageOk = $derived(ov && ov.storage.accounts.up && ov.storage.history.up)
  const queueLevel = (q) => (q > 5000 ? ['bad', 'Backed up'] : q > 500 ? ['warn', 'Busy'] : ['', 'Normal'])
  const kindName = { postgres: 'PostgreSQL', sqlite: 'SQLite', scylla: 'ScyllaDB', pebble: 'Pebble' }
</script>

<svelte:head><title>Bouncer overview · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>Admins only.</b> Your account doesn’t have access to the bouncer overview.</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>Bouncer overview</h1>
      <p>Everything running on {ov?.ircHost || session.config.ircHost} across all accounts: who’s online, which node holds them, and what needs a look.</p>
    </div>
    <div class="od-cluster">
      <a class="btn" href="/admin/users"><Icon name="users" />Manage users</a>
      <a class="btn btn-primary" href="/admin/users#new"><Icon name="plus" />New user</a>
    </div>
  </div>

  {#if err}<div class="callout bad"><Icon name="alert" /><span>Couldn’t load the overview: {err}</span></div>{/if}
  {#if !ov}
    {#if !err}<div class="tiles"><div class="skel tile"></div><div class="skel tile"></div><div class="skel tile"></div><div class="skel tile"></div></div>{/if}
  {:else}
    <div class="tiles enter">
      <a class="tile" href="/admin/users">
        <div class="tile-top"><span class="caps"><Icon name="users" class="i-sm" />Accounts</span><span class="chip">{ov.admins} admin{ov.admins === 1 ? '' : 's'}</span></div>
        <div class="od-stat"><span class="n" data-testid="ov-users">{ov.users}</span><span class="u">{ov.sso} sign in with SSO · {ov.suspended} suspended</span></div>
        <span class="ubar ice" role="img" aria-label="{ov.online} of {ov.users} accounts online"><span style="transform:scaleX({ratio(ov.online, ov.users)})"></span></span>
      </a>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="globe" class="i-sm" />Upstream links</span>
          {#if ov.linksTotal - ov.linksUp > 0}<span class="chip warn">{ov.linksTotal - ov.linksUp} down</span>{:else}<span class="chip ok">All up</span>{/if}</div>
        <div class="od-stat"><span class="n">{ov.linksUp}<small>/ {ov.linksTotal}</small></span><span class="u">connected right now</span></div>
        <span class="comb" style="--fill:{Math.round(ratio(ov.linksUp, ov.linksTotal) * 100)}%;width:100%" role="img" aria-label="{ov.linksUp} of {ov.linksTotal} upstream links connected"></span>
      </div>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="monitor" class="i-sm" />Clients</span><span class="chip ok live"><span class="dot" aria-hidden="true"></span>Live</span></div>
        <div class="od-stat"><span class="n">{ov.attachedClients}</span><span class="u">IRC client{ov.attachedClients === 1 ? '' : 's'}{ov.attachedClients ? ` across ${ov.accountsAttached} account${ov.accountsAttached === 1 ? '' : 's'}` : ''}{ov.webAttached ? ` · ${ov.webAttached} in the web app` : ''}</span></div>
        <span class="ubar" role="img" aria-label="{ov.attachedClients} clients attached"><span style="transform:scaleX({ratio(ov.accountsAttached, ov.users)});background:var(--nick)"></span></span>
      </div>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="server" class="i-sm" />Cluster</span>
          {#if !ov.clustered}<span class="chip">Single node</span>{:else if healthy}<span class="chip ok">Healthy</span>{:else}<span class="chip warn">Node stale</span>{/if}</div>
        <div class="od-stat"><span class="n">{Math.max(1, nodes.length)}</span><span class="u">node{nodes.length === 1 || !nodes.length ? '' : 's'} holding {placed.filter((p) => p.node).length} user{placed.filter((p) => p.node).length === 1 ? '' : 's'}</span></div>
        <span class="ubar ice" role="img" aria-label="{nodes.filter(nodeLive).length} of {nodes.length} nodes heartbeating"><span style="transform:scaleX({nodes.length ? ratio(nodes.filter(nodeLive).length, nodes.length) : 1})"></span></span>
      </div>
    </div>

    <div class="grid-main-side">
      <section class="card card-sheen enter" aria-labelledby="cl-h">
        <div class="section-head">
          <div class="od-stack"><span class="caps">Cluster placement</span><h2 id="cl-h">Nodes → users</h2></div>
          <div class="tabs status-tabs" role="tablist" aria-label="User filter">
            {#each Object.keys(label) as f}
              <button class="tab" role="tab" aria-selected={filter === f} onclick={() => (filter = f)}>{f === 'all' ? 'All' : f === 'online' ? 'Online' : 'Issues'} <span class="n">{(ov.placement || []).filter(match[f]).length}</span></button>
            {/each}
          </div>
        </div>
        {#if !nodes.length}
          <p class="desc">No node has checked in yet — give it a few seconds after a start.</p>
        {:else}
          <Topology left={leftNodes} right={shown} leftLabel="evrd nodes" rightLabel={label[filter]} empty="No accounts match this filter." strandsPer={6} single>
            {#snippet source(l)}
              {@const n = nodes.find((x) => x.id === l.id)}
              <div class="net-node" style="--net:{n.color}">
                <div class="head">
                  <span class="mono-badge" aria-hidden="true">{nodeBadge(n.id)}</span>
                  <span class="od-field od-fill"><span class="od-truncate">{n.id}</span><span class="od-truncate">{n.host || 'this server'} · up {since(n.startedAt + skew)}</span></span>
                  {#if nodeLive(n)}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>Live</span>{:else}<span class="chip warn">Stale</span>{/if}
                </div>
                <div class="node-meta">
                  <div class="od-stat"><span>{n.users}</span><span>Users</span></div>
                  <div class="od-stat" title="Upstream links connected"><span>{n.linksUp}</span><span>Links</span></div>
                  <div class="od-stat"><span>{ago(n.heartbeatAt + skew)}</span><span>Heartbeat</span></div>
                </div>
              </div>
            {/snippet}
            {#snippet item(u)}
              <a class="chan-node user-node" class:off={u.status === 'suspended'} href="/admin/users/{u.id}" style={nodes.find((n) => n.id === u.node) ? `--net:${nodes.find((n) => n.id === u.node).color}` : ''}>
                <span class="avatar" aria-hidden="true">{u.username[0].toUpperCase()}</span>
                <span class="od-field"><span class="od-truncate">{u.username}</span><span class="sub"><span class="od-nowrap">{u.node ? `${u.up}/${u.nets} links · ${u.status === 'online' ? 'client attached' : u.status === 'offline' ? 'no client attached' : u.status}` : 'not placed'}</span>{#if u.issue && u.status !== 'suspended'}<span class="od-nowrap" style="color:var(--signal)">{u.issue}</span>{/if}</span></span>
                {#if u.status === 'suspended'}<span class="chip bad"><Icon name="ban" />Suspended</span>
                {:else if u.issue}<span class="count hot" aria-label="Issue: {u.issue}">!</span>
                {:else}<span class="count calm" aria-label="{u.clients} clients attached">{u.clients}</span>{/if}
              </a>
            {/snippet}
          </Topology>
          {#if ov.clustered}
            <p class="desc" style="margin-top:var(--s4)">Each account lives on exactly one node, chosen by rendezvous hashing. Accounts with no networks or that are suspended aren’t placed anywhere.</p>
          {:else}
            <p class="desc" style="margin-top:var(--s4)">Clustering is off, so this node holds every account. To spread accounts over several nodes, set <span class="code">internal_listen</span> and the other cluster settings on each — see <a href="https://4evrd.github.io/docs.html#clustering">Running several nodes</a>.</p>
          {/if}
        {/if}
      </section>

      <div class="side-stack">
        <section class="anomaly" aria-labelledby="attn-h">
          <div class="od-row-top" style="justify-content:space-between">
            <h2 id="attn-h" class="title">Needs<br />attention</h2>
            <span class="count {ov.attention.length ? 'mention' : 'calm'}" data-testid="attn-count">{ov.attention.length}</span>
          </div>
          <div class="rows">
            {#each ov.attention as a}
              {@const [ic, tone] = attnIcon[a.kind] || ['alert', 'warn']}
              <a class="row attn-row" href={a.user ? `/admin/users?user=${encodeURIComponent(a.user)}` : '/admin/users'}>
                <span class="ic {tone}"><Icon name={ic} class="i-sm" /></span>
                <span class="od-field"><span class="name">{a.title}</span><span class="sub">{a.detail}{a.since ? ' · since ' + (now() - a.since > 86400 ? date(a.since) : clock(a.since)) : ''}</span></span>
                <Icon name="chevron" class="i-sm" />
              </a>
            {:else}
              <p class="desc">Nothing needs a look. Every link is up and nobody is stuck.</p>
            {/each}
          </div>
        </section>

        <section class="card tight card-sheen" aria-labelledby="def-h">
          <div class="section-head" style="margin-bottom:var(--s3)"><h2 id="def-h" class="caps">Server defaults</h2><span class="chip">evrd.conf</span></div>
          <div class="kv-list">
            <div class="kv"><div class="od-field"><span class="k">Networks per user</span><span class="v">{limit(ov.defaults.maxNetworks)}</span></div></div>
            <div class="kv"><div class="od-field"><span class="k">Clients per user</span><span class="v">{limit(ov.defaults.maxClients)}</span></div></div>
            <div class="kv"><div class="od-field"><span class="k">Backlog per channel</span><span class="v">{ov.defaults.backlogLines ? ov.defaults.backlogLines.toLocaleString('en') + ' lines' : 'Unlimited'}</span></div></div>
          </div>
          <p class="desc" style="margin-top:var(--s3);font-size:var(--fs-xs)">Change these in evrd.conf. Per-user limits override them on the Users page.</p>
        </section>
      </div>
    </div>

    <div class="grid-main-side">
      <section class="card enter" aria-labelledby="up-h">
        <div class="section-head">
          <div class="od-stack"><span class="caps">Upstream networks</span><h2 id="up-h">Where your users connect</h2></div>
          <span class="chip">{ov.upstreams.length} network{ov.upstreams.length === 1 ? '' : 's'} · {ov.networks} link{ov.networks === 1 ? '' : 's'}</span>
        </div>
        <div class="rows">
          {#each ov.upstreams as u (u.host)}
            <div class="row up-row" title={u.error || ''}>
              <span class="od-field"><span class="name">{u.name}</span><span>{u.host}</span></span>
              <span class="bar"><span class="comb" style="--fill:{Math.round((u.users / maxUsers) * 100)}%;width:100%" role="img" aria-label="{u.users} of {ov.users} users connect here"></span><span class="faint od-nowrap" style="font-size:var(--fs-xs)">{u.users} user{u.users === 1 ? '' : 's'} · {u.up} connected</span></span>
              {#if !u.enabled}<span class="chip">Off</span>
              {:else if !u.up}<span class="chip warn">All down</span>
              {:else if u.enabled - u.up > 0}<span class="chip warn">{u.enabled - u.up} down</span>
              {:else}<span class="chip ok">All up</span>{/if}
            </div>
          {:else}
            <p class="desc">No one has added a network yet.</p>
          {/each}
        </div>
      </section>

      <section class="card tight card-sheen enter" aria-labelledby="st-h">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <h2 id="st-h" class="caps">Storage</h2>
          {#if storageOk}<span class="chip ok"><span class="dot" aria-hidden="true"></span>OK</span>{:else}<span class="chip bad">Problem</span>{/if}
        </div>
        <div class="kv-list">
          <div class="kv"><div class="od-field"><span class="k">Accounts</span><span class="v">{kindName[ov.storage.accounts.kind] || ov.storage.accounts.kind}{ov.storage.accounts.addr ? ' · ' + ov.storage.accounts.addr : ''}</span></div><span class="chip {ov.storage.accounts.up ? 'ok' : 'bad'}">{ov.storage.accounts.up ? 'Up' : 'Down'}</span></div>
          <div class="kv"><div class="od-field"><span class="k">Chat history</span><span class="v">{kindName[ov.storage.history.kind] || ov.storage.history.kind}{ov.storage.history.addr ? ' · ' + ov.storage.history.addr : ''}</span></div><span class="chip {ov.storage.history.up ? 'ok' : 'bad'}">{ov.storage.history.up ? 'Up' : 'Down'}</span></div>
          {#if ov.storage.history.queue != null}
            {@const [tone, word] = queueLevel(ov.storage.history.queue)}
            <div class="kv"><div class="od-field"><span class="k">History write queue</span><span class="v">{ov.storage.history.queue.toLocaleString('en')} pending</span></div><span class="chip {tone}">{word}</span></div>
          {/if}
        </div>
      </section>
    </div>
  {/if}
{/if}

<style>
  .side-stack { display: grid; gap: var(--s6); align-content: start; }
  :global(.user-node) { grid-template-columns: 32px minmax(0, 1fr) auto; }
  :global(.user-node .avatar) { width: 32px; height: 32px; font-size: var(--fs-xs); }
  :global(.user-node.off) { opacity: 0.55; }
  .node-meta { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1.5fr); gap: var(--s2); }
  .node-meta .od-stat { min-width: 0; }
  .node-meta .od-stat span { overflow: hidden; text-overflow: ellipsis; }
  .node-meta .od-stat { padding: var(--s2) var(--s3); border-radius: var(--r-field); background: oklch(14% 0.011 215 / 0.55); }
  .node-meta .od-stat span:first-child { font-family: var(--font-mono); font-size: var(--fs-sm); white-space: nowrap; }
  .node-meta .od-stat span:last-child { font-size: var(--fs-2xs); color: var(--faint); text-transform: uppercase; letter-spacing: 0.06em; font-weight: var(--fw-med); white-space: nowrap; }
  .attn-row { --cols: 32px minmax(0, 1fr) auto; color: var(--fg); }
  .attn-row:hover { text-decoration: none; }
  .attn-row .ic { display: grid; place-items: center; width: 32px; height: 32px; border-radius: 9px; background: var(--bg-deep); }
  .attn-row .ic.bad { color: var(--bad); } .attn-row .ic.warn { color: var(--warn); } .attn-row .ic.signal { color: var(--signal); }
  .up-row { --cols: minmax(120px, 1.2fr) minmax(0, 2fr) auto; }
  .up-row .bar { display: grid; gap: 6px; }
  .up-row .od-field span:last-child { font-family: var(--font-mono); font-size: var(--fs-xs); color: var(--muted); }
  @media (max-width: 639px) { .up-row { --cols: minmax(0, 1fr) auto; } .up-row .bar { grid-column: 1 / -1; grid-row: 2; } }
</style>
