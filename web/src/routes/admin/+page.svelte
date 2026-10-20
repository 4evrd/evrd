<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Topology from '$lib/components/Topology.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { ago, since, clock, date, limit, ratio, netColor, nodeBadge } from '$lib/format.js'
  import Msg from '$lib/components/Msg.svelte'
  import { t, i18n } from '$lib/i18n.svelte.js'

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
  const label = $derived({ all: t('admin.accounts'), online: t('admin.onlineNow'), issue: t('admin.accountsWithIssue') })
  const tabName = { all: 'admin.tabAll', online: 'admin.tabOnline', issue: 'admin.tabIssues' }
  const shown = $derived((ov?.placement || []).filter(match[filter]).map((p) => ({ ...p, key: p.id, source: p.node || '-', dashed: p.status !== 'online' })))
  const leftNodes = $derived(nodes.map((n) => ({ id: n.id, color: n.color })))

  const attnIcon = { network: ['alert', 'signal'], limit: ['sliders', 'warn'], suspended: ['ban', 'bad'], inactive: ['user', 'warn'] }
  const maxUsers = $derived(Math.max(1, ...(ov?.upstreams || []).map((u) => u.users)))
  const storageOk = $derived(ov && ov.storage.accounts.up && ov.storage.history.up)
  const queueLevel = (q) => (q > 5000 ? ['bad', t('admin.queueBackedUp')] : q > 500 ? ['warn', t('admin.queueBusy')] : ['', t('admin.queueNormal')])
  const kindName = { postgres: 'PostgreSQL', sqlite: 'SQLite', scylla: 'ScyllaDB', pebble: 'Pebble' }
</script>

<svelte:head><title>{t('admin.pageTitle')}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('admin.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('admin.heading')}</h1>
      <p>{t('admin.intro', { host: ov?.ircHost || session.config.ircHost })}</p>
    </div>
    <div class="od-cluster">
      <a class="btn" href="/admin/users"><Icon name="users" />{t('admin.manageUsers')}</a>
      <a class="btn btn-primary" href="/admin/users#new"><Icon name="plus" />{t('adminUsers.newUser')}</a>
    </div>
  </div>

  {#if err}<div class="callout bad"><Icon name="alert" /><span>{t('admin.loadFailed', { error: err })}</span></div>{/if}
  {#if !ov}
    {#if !err}<div class="tiles"><div class="skel tile"></div><div class="skel tile"></div><div class="skel tile"></div><div class="skel tile"></div></div>{/if}
  {:else}
    <div class="tiles enter">
      <a class="tile" href="/admin/users">
        <div class="tile-top"><span class="caps"><Icon name="users" class="i-sm" />{t('admin.accounts')}</span><span class="chip">{t('admin.nAdmins', { count: ov.admins })}</span></div>
        <div class="od-stat"><span class="n" data-testid="ov-users">{ov.users}</span><span class="u">{t('admin.accountsSub', { sso: ov.sso, suspended: ov.suspended })}</span></div>
        <span class="ubar ice" role="img" aria-label={t('admin.accountsOnlineAria', { online: ov.online, users: ov.users })}><span style="transform:scaleX({ratio(ov.online, ov.users)})"></span></span>
      </a>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="globe" class="i-sm" />{t('admin.upstreamLinks')}</span>
          {#if ov.linksTotal - ov.linksUp > 0}<span class="chip warn">{t('admin.nDown', { count: ov.linksTotal - ov.linksUp })}</span>{:else}<span class="chip ok">{t('admin.allUp')}</span>{/if}</div>
        <div class="od-stat"><span class="n">{ov.linksUp}<small>/ {ov.linksTotal}</small></span><span class="u">{t('admin.connectedNow')}</span></div>
        <span class="comb" style="--fill:{Math.round(ratio(ov.linksUp, ov.linksTotal) * 100)}%;width:100%" role="img" aria-label={t('admin.linksAria', { up: ov.linksUp, total: ov.linksTotal })}></span>
      </div>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="monitor" class="i-sm" />{t('admin.clients')}</span><span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('admin.live')}</span></div>
        <div class="od-stat"><span class="n">{ov.attachedClients}</span><span class="u">{ov.attachedClients ? t('admin.ircClientsAcross', { count: ov.attachedClients, accounts: t('admin.nAccounts', { count: ov.accountsAttached }) }) : t('admin.ircClients', { count: ov.attachedClients })}{ov.webAttached ? ' · ' + t('admin.inWebApp', { count: ov.webAttached }) : ''}</span></div>
        <span class="ubar" role="img" aria-label={t('admin.clientsAttachedAria', { count: ov.attachedClients })}><span style="transform:scaleX({ratio(ov.accountsAttached, ov.users)});background:var(--nick)"></span></span>
      </div>
      <div class="tile">
        <div class="tile-top"><span class="caps"><Icon name="server" class="i-sm" />{t('admin.cluster')}</span>
          {#if !ov.clustered}<span class="chip">{t('admin.singleNode')}</span>{:else if healthy}<span class="chip ok">{t('admin.healthy')}</span>{:else}<span class="chip warn">{t('admin.nodeStale')}</span>{/if}</div>
        <div class="od-stat"><span class="n">{Math.max(1, nodes.length)}</span><span class="u">{t('admin.nodesHolding', { count: Math.max(1, nodes.length), users: t('admin.nUsers', { count: placed.filter((p) => p.node).length }) })}</span></div>
        <span class="ubar ice" role="img" aria-label={t('admin.heartbeatAria', { live: nodes.filter(nodeLive).length, total: nodes.length })}><span style="transform:scaleX({nodes.length ? ratio(nodes.filter(nodeLive).length, nodes.length) : 1})"></span></span>
      </div>
    </div>

    <div class="grid-main-side">
      <section class="card card-sheen enter" aria-labelledby="cl-h">
        <div class="section-head">
          <div class="od-stack"><span class="caps">{t('admin.placement')}</span><h2 id="cl-h">{t('admin.nodesToUsers')}</h2></div>
          <div class="tabs status-tabs" role="tablist" aria-label={t('admin.userFilter')}>
            {#each Object.keys(label) as f}
              <button class="tab" role="tab" aria-selected={filter === f} onclick={() => (filter = f)}>{t(tabName[f])} <span class="n">{(ov.placement || []).filter(match[f]).length}</span></button>
            {/each}
          </div>
        </div>
        {#if !nodes.length}
          <p class="desc">{t('admin.noNodes')}</p>
        {:else}
          <Topology left={leftNodes} right={shown} leftLabel={t('admin.evrdNodes')} rightLabel={label[filter]} empty={t('admin.noMatch')} strandsPer={6} single>
            {#snippet source(l)}
              {@const n = nodes.find((x) => x.id === l.id)}
              <div class="net-node" style="--net:{n.color}">
                <div class="head">
                  <span class="mono-badge" aria-hidden="true">{nodeBadge(n.id)}</span>
                  <span class="od-field od-fill"><span class="od-truncate">{n.id}</span><span class="od-truncate">{t('admin.nodeUp', { host: n.host || t('admin.thisServer'), time: since(n.startedAt + skew) })}</span></span>
                  {#if nodeLive(n)}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('admin.live')}</span>{:else}<span class="chip warn">{t('admin.stale')}</span>{/if}
                </div>
                <div class="node-meta">
                  <div class="od-stat"><span>{n.users}</span><span>{t('admin.users')}</span></div>
                  <div class="od-stat" title={t('admin.linksConnectedTitle')}><span>{n.linksUp}</span><span>{t('admin.links')}</span></div>
                  <div class="od-stat"><span>{ago(n.heartbeatAt + skew)}</span><span>{t('admin.heartbeat')}</span></div>
                </div>
              </div>
            {/snippet}
            {#snippet item(u)}
              <a class="chan-node user-node" class:off={u.status === 'suspended'} href="/admin/users/{u.id}" style={nodes.find((n) => n.id === u.node) ? `--net:${nodes.find((n) => n.id === u.node).color}` : ''}>
                <span class="avatar" aria-hidden="true">{u.username[0].toUpperCase()}</span>
                <span class="od-field"><span class="od-truncate">{u.username}</span><span class="sub"><span class="od-nowrap">{u.node ? t('admin.userLinks', { up: u.up, nets: u.nets, state: u.status === 'online' ? t('admin.clientAttached') : u.status === 'offline' ? t('admin.noClientAttached') : u.status === 'suspended' ? t('admin.statusSuspended') : u.status }) : t('admin.notPlaced')}</span>{#if u.issue && u.status !== 'suspended'}<span class="od-nowrap" style="color:var(--signal)">{u.issue}</span>{/if}</span></span>
                {#if u.status === 'suspended'}<span class="chip bad"><Icon name="ban" />{t('admin.suspended')}</span>
                {:else if u.issue}<span class="count hot" aria-label={t('admin.issueAria', { issue: u.issue })}>!</span>
                {:else}<span class="count calm" aria-label={t('admin.clientsAttachedAria', { count: u.clients })}>{u.clients}</span>{/if}
              </a>
            {/snippet}
          </Topology>
          {#if ov.clustered}
            <p class="desc" style="margin-top:var(--s4)">{t('admin.clusteredHelp')}</p>
          {:else}
            <p class="desc" style="margin-top:var(--s4)"><Msg key="admin.singleHelp">{#snippet setting()}<span class="code">internal_listen</span>{/snippet}{#snippet docs()}<a href="https://4evrd.github.io/docs.html#clustering">{t('admin.docsLink')}</a>{/snippet}</Msg></p>
          {/if}
        {/if}
      </section>

      <div class="side-stack">
        <section class="anomaly" aria-labelledby="attn-h">
          <div class="od-row-top" style="justify-content:space-between">
            <h2 id="attn-h" class="title"><Msg key="admin.needsAttention">{#snippet br()}<br />{/snippet}</Msg></h2>
            <span class="count {ov.attention.length ? 'mention' : 'calm'}" data-testid="attn-count">{ov.attention.length}</span>
          </div>
          <div class="rows">
            {#each ov.attention as a}
              {@const [ic, tone] = attnIcon[a.kind] || ['alert', 'warn']}
              <a class="row attn-row" href={a.user ? `/admin/users?user=${encodeURIComponent(a.user)}` : '/admin/users'}>
                <span class="ic {tone}"><Icon name={ic} class="i-sm" /></span>
                <span class="od-field"><span class="name">{a.title}</span><span class="sub">{a.detail}{a.since ? ' · ' + t('admin.since', { time: now() - a.since > 86400 ? date(a.since) : clock(a.since) }) : ''}</span></span>
                <Icon name="chevron" class="i-sm" />
              </a>
            {:else}
              <p class="desc">{t('admin.allClear')}</p>
            {/each}
          </div>
        </section>

        <section class="card tight card-sheen" aria-labelledby="def-h">
          <div class="section-head" style="margin-bottom:var(--s3)"><h2 id="def-h" class="caps">{t('admin.serverDefaults')}</h2><span class="chip">evrd.conf</span></div>
          <div class="kv-list">
            <div class="kv"><div class="od-field"><span class="k">{t('admin.networksPerUser')}</span><span class="v">{limit(ov.defaults.maxNetworks)}</span></div></div>
            <div class="kv"><div class="od-field"><span class="k">{t('admin.clientsPerUser')}</span><span class="v">{limit(ov.defaults.maxClients)}</span></div></div>
            <div class="kv"><div class="od-field"><span class="k">{t('admin.backlogPerChannel')}</span><span class="v">{ov.defaults.backlogLines ? t('admin.nLines', { lines: ov.defaults.backlogLines.toLocaleString(i18n.intl) }) : t('admin.unlimited')}</span></div></div>
          </div>
          <p class="desc" style="margin-top:var(--s3);font-size:var(--fs-xs)">{t('admin.defaultsHelp')}</p>
        </section>
      </div>
    </div>

    <div class="grid-main-side">
      <section class="card enter" aria-labelledby="up-h">
        <div class="section-head">
          <div class="od-stack"><span class="caps">{t('admin.upstreamNetworks')}</span><h2 id="up-h">{t('admin.whereUsersConnect')}</h2></div>
          <span class="chip">{t('admin.nNetworks', { count: ov.upstreams.length })} · {t('admin.nLinks', { count: ov.networks })}</span>
        </div>
        <div class="rows">
          {#each ov.upstreams as u (u.host)}
            <div class="row up-row" title={u.error || ''}>
              <span class="od-field"><span class="name">{u.name}</span><span>{u.host}</span></span>
              <span class="bar"><span class="comb" style="--fill:{Math.round((u.users / maxUsers) * 100)}%;width:100%" role="img" aria-label={t('admin.connectHereAria', { users: u.users, total: ov.users })}></span><span class="faint od-nowrap" style="font-size:var(--fs-xs)">{t('admin.usersConnected', { count: u.users, up: u.up })}</span></span>
              {#if !u.enabled}<span class="chip">{t('admin.off')}</span>
              {:else if !u.up}<span class="chip warn">{t('admin.allDown')}</span>
              {:else if u.enabled - u.up > 0}<span class="chip warn">{t('admin.nDown', { count: u.enabled - u.up })}</span>
              {:else}<span class="chip ok">{t('admin.allUp')}</span>{/if}
            </div>
          {:else}
            <p class="desc">{t('admin.noUpstreams')}</p>
          {/each}
        </div>
      </section>

      <section class="card tight card-sheen enter" aria-labelledby="st-h">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <h2 id="st-h" class="caps">{t('admin.storage')}</h2>
          {#if storageOk}<span class="chip ok"><span class="dot" aria-hidden="true"></span>{t('admin.ok')}</span>{:else}<span class="chip bad">{t('admin.problem')}</span>{/if}
        </div>
        <div class="kv-list">
          <div class="kv"><div class="od-field"><span class="k">{t('admin.accounts')}</span><span class="v">{kindName[ov.storage.accounts.kind] || ov.storage.accounts.kind}{ov.storage.accounts.addr ? ' · ' + ov.storage.accounts.addr : ''}</span></div><span class="chip {ov.storage.accounts.up ? 'ok' : 'bad'}">{ov.storage.accounts.up ? t('admin.up') : t('admin.down')}</span></div>
          <div class="kv"><div class="od-field"><span class="k">{t('admin.chatHistory')}</span><span class="v">{kindName[ov.storage.history.kind] || ov.storage.history.kind}{ov.storage.history.addr ? ' · ' + ov.storage.history.addr : ''}</span></div><span class="chip {ov.storage.history.up ? 'ok' : 'bad'}">{ov.storage.history.up ? t('admin.up') : t('admin.down')}</span></div>
          {#if ov.storage.history.queue != null}
            {@const [tone, word] = queueLevel(ov.storage.history.queue)}
            <div class="kv"><div class="od-field"><span class="k">{t('admin.writeQueue')}</span><span class="v">{t('admin.nPending', { count: ov.storage.history.queue.toLocaleString(i18n.intl) })}</span></div><span class="chip {tone}">{word}</span></div>
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
