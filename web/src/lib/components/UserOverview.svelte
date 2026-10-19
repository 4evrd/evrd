<script>
  // A user's overview: tiles, networks → channels, traffic, link health,
  // recent events and attached clients. The user's own dashboard, and an
  // admin's view of any user (admin: links stay on the admin page).
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import Ticks from '$lib/components/Ticks.svelte'
  import Topology from '$lib/components/Topology.svelte'
  import TrafficChart from '$lib/components/TrafficChart.svelte'
  import Msg from '$lib/components/Msg.svelte'
  import { short, netColor, ratio, level, kfmt, clock, since, limit } from '$lib/format.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  let { ov = null, usage = { networks: 0 }, limits = {}, admin = false, who = 'you' } = $props()

  const nets = $derived((ov?.networks || []).map((n, i) => ({ ...n, color: netColor(i), short: short(n.name) })))
  const connected = $derived(nets.filter((n) => n.status === 'connected').length)
  const retrying = $derived(nets.filter((n) => n.status === 'reconnecting' || n.status === 'connecting').length)
  const clients = $derived(ov?.clients || [])
  const ircClients = $derived(clients.filter((c) => c.kind === 'irc').length)
  const mentions = $derived(ov?.health?.mentions || 0)
  // where things link: the user's own pages, or anchors on the admin page
  const href = (own, anchor) => (admin ? anchor : own)

  // topology: channels hang off their network
  let filter = $state('all')
  const allChans = $derived(nets.flatMap((n) => (n.buffers || []).map((b) => ({ ...b, key: `${n.id}/${b.name}`, source: n.id, net: n, dashed: n.status !== 'connected' }))))
  const match = { all: () => true, unread: (c) => c.unread > 0 || c.mentions > 0, mention: (c) => c.mentions > 0 }
  const shown = $derived(allChans.filter(match[filter]))
  const counts = $derived({ all: allChans.length, unread: allChans.filter(match.unread).length, mention: allChans.filter(match.mention).length })
  const chanLabel = $derived({ all: t('overview.chansAll'), unread: t('overview.chansUnread'), mention: t('overview.chansMention') })

  // traffic chart
  let focus = $state(null) // a network hovered in the legend
  const series = $derived((ov?.traffic?.series || []).map((s) => {
    const i = nets.findIndex((n) => n.id === s.network)
    return { name: s.name, color: i === 0 ? 'var(--ice)' : netColor(Math.max(i, 0)), points: s.points }
  }).sort((a, b) => b.points.reduce((x, y) => x + y, 0) - a.points.reduce((x, y) => x + y, 0)))
  const markers = $derived((ov?.events || []).filter((e) => ['disconnected', 'netsplit', 'attached'].includes(e.kind)).slice(0, 4).map((e) => ({
    at: e.at, tone: e.kind === 'disconnected' ? 'bad' : e.kind === 'netsplit' ? 'warn' : 'ok',
    label: e.title.replace(/ (ping timeout|connection .*|can't .*|attached)$/i, '').toUpperCase() + ' · ' + t(e.kind === 'disconnected' ? 'overview.markerLost' : e.kind === 'netsplit' ? 'overview.markerNetsplit' : 'overview.markerAttach'),
  })))
  const spark = $derived.by(() => {
    if (!series.length) return ''
    const tot = series[0].points.map((_, i) => series.reduce((a, s) => a + (s.points[i] || 0), 0))
    const m = Math.max(...tot) || 1
    return tot.map((v, i) => `${(i / (tot.length - 1)) * 120},${30 - (v / m) * 26}`).join(' ')
  })
  const h = $derived(ov?.health || {})
  const clientIcon = (c) => (c.kind === 'web' ? 'globe' : /irssi|weechat|senpai|catgirl/i.test(c.software) ? 'terminal' : 'monitor')
</script>

<div class="tiles enter">
  <a class="tile" href={href('/networks', '#networks')}>
    <div class="tile-top"><span class="caps"><Icon name="globe" class="i-sm" />{t('overview.networks')}</span><span class="chip">{t('overview.usedOf', { used: usage.networks, max: limit(limits.maxNetworks) ?? '' })}</span></div>
    <div class="od-stat"><span class="n" data-testid="tile-networks">{usage.networks}<small>/ {limit(limits.maxNetworks)}</small></span><span class="u">{t('overview.networksSetUp')}</span></div>
    <span class="ubar {level(usage.networks, limits.maxNetworks)}" role="img" aria-label={t('overview.networkLimit', { pct: Math.round(ratio(usage.networks, limits.maxNetworks) * 100) })}><span style="transform:scaleX({ratio(usage.networks, limits.maxNetworks)})"></span></span>
  </a>
  <a class="tile" href={href('/networks?filter=connected', '#networks')}>
    <div class="tile-top"><span class="caps"><Icon name="activity" class="i-sm" />{t('overview.connected')}</span>{#if connected}<span class="chip ok live"><span class="dot" aria-hidden="true"></span>{t('overview.live')}</span>{:else}<span class="chip">{t('overview.idle')}</span>{/if}</div>
    <div class="od-stat"><span class="n" data-testid="tile-connected">{connected}<small>/ {nets.length}</small></span><span class="u">{retrying ? t('overview.onlineReconnecting', { count: retrying }) : t('overview.online')}</span></div>
    <span class="ticks ok" class:dense={nets.length > 40} role="img" aria-label={t('overview.connectedAria', { connected, reconnecting: retrying })}>{#each nets as n}<i class:on={n.status !== 'disconnected'} style={n.status === 'reconnecting' || n.status === 'connecting' ? 'background:var(--warn)' : ''}></i>{/each}</span>
  </a>
  <a class="tile" href={href('/clients', '#clients')}>
    <div class="tile-top"><span class="caps"><Icon name="monitor" class="i-sm" />{t('overview.clients')}</span><span class="chip nick">{t('overview.attachedCount', { count: clients.length })}</span></div>
    <div class="od-stat"><span class="n" data-testid="tile-clients">{ircClients}<small>/ {limit(limits.maxClients)}</small></span><span class="u od-truncate">{clients.length ? [...new Set(clients.map((c) => (c.kind === 'web' ? t('overview.clientWeb') : c.software || t('overview.ircClient'))))].join(', ') : t('overview.noneAttached')}</span></div>
    <span class="ubar" role="img" aria-label={t('overview.ircClientsAria', { used: ircClients, max: limit(limits.maxClients) ?? '' })}><span style="transform:scaleX({ratio(ircClients, limits.maxClients)});background:var(--nick)"></span></span>
  </a>
  <div class="tile">
    <div class="tile-top"><span class="caps"><Icon name="message" class="i-sm" />{t('overview.messages24h')}</span>{#if mentions}<span class="chip warn">{t('overview.mentions', { count: mentions })}</span>{/if}</div>
    <div class="od-stat"><span class="n" data-testid="tile-messages">{ov?.traffic?.available ? kfmt(ov.traffic.total24h) : '—'}</span><span class="u">{ov?.traffic?.available ? (admin ? t('overview.linesBuffered') : t('overview.linesBufferedYou')) : t('overview.noMetrics')}</span></div>
    <svg class="spark" viewBox="0 0 120 32" preserveAspectRatio="none" aria-hidden="true">
      <defs><linearGradient id="spg" x1="0" x2="1" y1="0" y2="0"><stop offset="0" style="stop-color:var(--signal)" /><stop offset="1" style="stop-color:var(--cyan)" /></linearGradient></defs>
      {#if spark}<polyline points={spark} fill="none" style="stroke:url(#spg)" stroke-width="1.5" vector-effect="non-scaling-stroke" />{/if}
    </svg>
  </div>
</div>

<section class="card topo-card enter" aria-labelledby="topo-h">
  <div class="section-head" style="margin-bottom:var(--s5)">
    <div class="od-stack"><span class="caps">{t('overview.liveTopology')}</span><h2 id="topo-h">{t('overview.topology')}</h2></div>
    <div class="tabs status-tabs" role="tablist" aria-label={t('overview.channelFilter')}>
      {#each [['all', t('overview.filterAll')], ['unread', t('overview.filterUnread')], ['mention', t('overview.filterMentions')]] as [k, l]}
        <button class="tab" role="tab" aria-selected={filter === k} onclick={() => (filter = k)}>{l} <span class="n">{counts[k]}</span></button>
      {/each}
    </div>
  </div>
  {#if !ov}
    <div class="skel"></div><div class="skel" style="margin-top:8px"></div>
  {:else if !nets.length}
    <div class="empty"><span class="ic"><Icon name="globe" /></span><h3>{t('overview.noNetworks')}</h3>{#if admin}<p>{t('overview.userNoNetworks', { who })}</p>{:else}<p>{t('overview.addNetworksHelp')}</p><a class="btn btn-primary btn-sm" href="/networks/new"><Icon name="plus" />{t('overview.addNetwork')}</a>{/if}</div>
  {:else}
    <Topology left={nets.map((n) => ({ id: n.id, color: n.color }))} right={shown} leftLabel={t('overview.upstreamNetworks')} rightLabel={chanLabel[filter]}
      empty={filter === 'all' ? t('overview.noChannels') : t('overview.allChannelsRead')}>
      {#snippet source(l, dim)}
        {@const n = nets.find((x) => x.id === l.id)}
        <a class="net-node" class:muted-node={filter !== 'all' && !shown.some((c) => c.source === n.id)} href={href(`/networks#net-${n.id}`, `#net-${n.id}`)} style="--net:{n.color}">
          <div class="head">
            <span class="mono-badge" aria-hidden="true">{n.short}</span>
            <span class="od-field od-fill"><span class="od-truncate">{n.name}</span><span class="od-truncate">{n.host}:{n.port}{n.tls ? ' · TLS' : ''}</span></span>
            <StatusChip status={n.status} />
          </div>
          {#if n.status === 'disconnected'}
            <p class="desc" style="font-size:var(--fs-xs)">{t('overview.notConnected')}</p>
          {:else}
            <div class="od-row" style="justify-content:space-between">
              <span class="faint od-nowrap" style="font-size:var(--fs-xs)">{t('overview.channels', { count: n.buffers.length })} · {n.lag != null ? n.lag + ' ms' : n.status === 'connected' ? t('overview.measuring') : n.lastError || t('overview.noLink')}</span>
              <span class="comb" style="--fill:{Math.max(8, n.trafficPct + 30)}%" role="img" aria-label={t('overview.trafficShare', { pct: n.trafficPct })}></span>
            </div>
            <div class="bars">
              {#each [[t('overview.barTraffic'), n.trafficPct, 'ice'], [t('overview.barBacklog'), n.backlogPct, 'warn'], [t('overview.barRead'), n.readPct, 'ice']] as [lbl, p, c]}
                <div class="bar-row"><span class="pct">{p}%</span><span class="lbl">{lbl}</span><span class="ubar {c}" aria-hidden="true"><span style="transform:scaleX({p / 100})"></span></span></div>
              {/each}
            </div>
          {/if}
        </a>
      {/snippet}
      {#snippet item(c, dim)}
        <svelte:element this={admin ? 'div' : 'a'} class="chan-node" class:dim href={admin ? undefined : `/chat?net=${c.source}&buf=${encodeURIComponent(c.name)}`} style="--net:{c.net.color}">
          <span class="hash"><Icon name="hash" class="i-sm" /></span>
          <span class="od-field"><span class="od-truncate">{c.name}</span><span class="sub"><span class="od-nowrap">{c.net.name}{c.members != null ? ' · ' + t('overview.users', { count: c.members, n: c.members.toLocaleString(i18n.locale) }) : ''}</span><Ticks level={c.rate} tone="heat" /></span></span>
          {#if c.mentions}<span class="count mention" aria-label={t('overview.mentionsAria', { count: c.mentions })}>{c.mentions}</span>
          {:else if c.unread}<span class="count" class:hot={c.unread > 30} aria-label={t('overview.unreadAria', { count: c.unread })}>{c.unread}</span>
          {:else}<span class="count calm" aria-label={t('overview.allRead')}>0</span>{/if}
        </svelte:element>
      {/snippet}
    </Topology>
  {/if}
</section>

<div class="grid-main-side">
  <section class="card" aria-labelledby="traffic-h">
    <div class="section-head">
      <div class="od-stack"><span class="caps">{t('overview.messageTraffic')}</span><h2 id="traffic-h">{t('overview.linesPerHour')}</h2></div>
      <div class="legend">{#each series as s}<button type="button" class:dim={focus && focus !== s.name} onpointerenter={() => (focus = s.name)} onpointerleave={() => (focus = null)} onfocus={() => (focus = s.name)} onblur={() => (focus = null)}><i style="--c:{s.color}"></i>{s.name}</button>{/each}</div>
    </div>
    {#if !ov}
      <div class="skel" style="height:240px"></div>
    {:else if !ov.traffic.available}
      <div class="chart-empty"><div class="od-stack" style="align-items:center"><Icon name="chart" /><span>{t('overview.needMetrics')}</span><span class="faint" style="font-size:var(--fs-xs)">{ov.traffic.error}</span></div></div>
    {:else if !series.length}
      <div class="chart-empty">{t('overview.noTraffic')}</div>
    {:else}
      <TrafficChart {series} start={ov.traffic.start} {markers} {focus} />
    {/if}
  </section>

  <div class="side-stack" style="display:grid;gap:var(--s6)">
    <section class="anomaly" aria-labelledby="health-h">
      <div class="od-row-top" style="justify-content:space-between">
        <h2 id="health-h" class="title"><Msg key="overview.upstreamHealth">{#snippet br()}<br />{/snippet}</Msg></h2>
        <a class="icon-btn" href={href('/networks', '#networks')} aria-label={t('overview.openNetworks')}><Icon name="external" /></a>
      </div>
      <div class="metric">
        <div class="od-row"><span>{t('overview.avgLag')}</span><span class="od-nowrap">{h.avgLag != null ? h.avgLag + ' ms' : '—'}</span></div>
        <span class="ubar ice" role="img" aria-label={t('overview.avgLag')}><span style="transform:scaleX({Math.min(1, (h.avgLag || 0) / 300)})"></span></span>
      </div>
      <div class="metric">
        <div class="od-row"><span>{t('overview.backlogUsed')}</span><span class="od-nowrap">{h.backlogPct ?? 0}%</span></div>
        <span class="ubar warn" role="img" aria-label={t('overview.backlogUsedAria', { pct: h.backlogPct ?? 0 })}><span style="transform:scaleX({(h.backlogPct || 0) / 100})"></span></span>
      </div>
      <div class="metric">
        <div class="od-row"><a class="metric-link" href={href('/reconnects', '#reconnects')} data-testid="reconnects-link">{t('overview.reconnects24h')}<Icon name="chevron" class="i-sm" /></a><span class="od-nowrap">{h.reconnects24h ?? '—'}</span></div>
        <span class="ubar bad" role="img" aria-label={t('overview.reconnectsAria')}><span style="transform:scaleX({Math.min(1, (h.reconnects24h || 0) / 6)})"></span></span>
      </div>
      <div class="od-row" style="justify-content:space-between;font-size:var(--fs-sm)"><span>{t('overview.offline24h')}</span><span class="count calm od-nowrap">{h.offlineMin24h != null ? t('overview.min', { count: h.offlineMin24h }) : '—'}</span></div>
      <div class="od-row" style="justify-content:space-between;font-size:var(--fs-sm)"><span>{t('overview.mentionsWaiting')}</span><span class="count mention">{mentions}</span></div>
    </section>

    <section class="card tight card-sheen" aria-labelledby="ev-h">
      <div class="section-head" style="margin-bottom:var(--s2)"><h2 id="ev-h" class="caps">{t('overview.recentEvents')}</h2></div>
      <div class="event-list">
        {#each (ov?.events || []).slice(0, 5) as e}
          <div class="event"><time>{clock(e.at)}</time><div class="od-field"><span>{e.title}</span><span>{e.detail}</span></div></div>
        {:else}
          <p class="desc" style="font-size:var(--fs-xs)">{t('overview.nothing24h')}</p>
        {/each}
      </div>
    </section>

    <section class="card tight" aria-labelledby="att-h">
      <div class="section-head" style="margin-bottom:var(--s3)">
        <h2 id="att-h" style="font-size:var(--fs-md)">{t('overview.attachedNow')}</h2>
        {#if !admin}<a class="btn btn-ghost btn-sm" href="/clients">{t('overview.manage')}</a>{/if}
      </div>
      <div class="rows">
        {#each clients as c (c.id)}
          <div class="row client-row" style="--cols:36px minmax(0,1fr) auto">
            <span class="cic"><Icon name={clientIcon(c)} /></span>
            <span class="od-field"><span class="name">{c.kind === 'web' ? t('overview.web') : c.software || t('overview.ircClient')}</span><span class="sub od-truncate">{c.networks.join(', ')}{c.kind === 'web' ? ' · ' + (c.software || t('overview.browser')) : c.device ? ' · ' + c.device : ''}</span></span>
            <span class="chip ok">{since(c.since)}</span>
          </div>
        {:else}
          <p class="desc" style="font-size:var(--fs-sm)">{#if admin}{t('overview.noClients')}{:else}<Msg key="overview.noClientsSetUp">{#snippet link()}<a href="/clients">{t('overview.setOneUp')}</a>{/snippet}</Msg>{/if}</p>
        {/each}
      </div>
    </section>
  </div>
</div>

<style>
  .cic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--cyan); }
  :global(.net-node.muted-node) { opacity: 0.45; }
</style>
