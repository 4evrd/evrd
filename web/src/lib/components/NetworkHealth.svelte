<script>
  // A network's health: its live link and settings at a glance, how the link
  // held up over a range (uptime, lag, messages and disconnects per slot),
  // its recent events and its channels. The user's own page, and an admin's
  // view of any user's network.
  //
  // data:     /api/networks/{id}/health (or the admin's variant)
  // range:    the range shown; onrange(r) picks another
  // logHref:  where the network's connection log is
  // chatHref: (channel) => where to read it (null: no links, for admins)
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import MetricChart from '$lib/components/MetricChart.svelte'
  import SlotBars from '$lib/components/SlotBars.svelte'
  import UptimeBand from '$lib/components/UptimeBand.svelte'
  import { RANGES, slotTimes, ticks, uptimeTone, lagTone } from '$lib/health.js'
  import { kfmt, clock, date, since, ago, wait, cap } from '$lib/format.js'
  import { METHODS, authLabel } from '$lib/services.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  let { data = null, range = '24h', onrange = () => {}, logHref = '', chatHref = null, loading = false } = $props()

  const net = $derived(data?.network || {})
  const live = $derived(data?.live || {})
  const sum = $derived(data?.summary || {})
  const se = $derived(data?.series || {})
  const n = $derived(data?.points || 0)
  const step = $derived(data?.step || 3600)
  const metrics = $derived(!!data?.metrics)
  const connected = $derived(live.status === 'connected')
  const rangeName = $derived({ '24h': t('health.range24h'), '7d': t('health.range7d'), '30d': t('health.range30d') })
  const over = $derived({ '24h': t('health.over24h'), '7d': t('health.over7d'), '30d': t('health.over30d') }[data?.range || range])

  // what slot i covers: "14:00–15:00", "Mon 14:00–15:00", "Mar 2"
  const weekday = (u) => new Date(u * 1000).toLocaleDateString(i18n.intl, { weekday: 'short' })
  function slotLabel(i) {
    const s = slotTimes(data.start, step, i, n)
    if (step >= 86400) return date(s.from + step / 2) + (s.current ? ' · ' + t('health.soFar') : '')
    const day = n > 24 ? weekday(s.from) + ' ' : ''
    return day + (s.current ? t('format.untilNow', { from: clock(s.from) }) : `${clock(s.from)}–${clock(s.to)}`)
  }
  const axis = $derived(data ? ticks(n, step).map((i) => [i, i === n ? t('format.now') : step >= 86400 ? date(data.start + i * step + step / 2) : n > 24 ? weekday(data.start + i * step) : clock(data.start + i * step)]) : [])

  const lines = $derived(metrics ? [{ name: t('health.messages'), color: 'var(--cyan)', points: se.lines }] : [])
  const drops = $derived(metrics ? [{ name: t('health.dropped'), color: 'var(--bad)', points: se.dropped }, { name: t('health.failed'), color: 'var(--warn)', points: se.failed }] : [])
  const lag = $derived(metrics ? [{ name: t('health.lagAvg'), points: se.lagAvg }, { name: t('health.lagWorst'), points: se.lagMax, color: 'var(--warn)' }] : [])
  const hasLag = $derived(metrics && se.lagAvg?.some((v) => v != null))
  const disconnects = $derived((sum.dropped || 0) + (sum.failed || 0))
  const ms = (v) => (v == null ? '—' : t('health.ms', { ms: Math.round(v).toLocaleString(i18n.intl) }))
  const pct = (v) => (v == null ? '—' : Math.round(v * 1000) / 10 + '%')

  const KIND = $derived({ disconnected: ['bad', t('health.kindLost')], failed: ['warn', t('health.kindFailed')], connected: ['ok', t('health.kindConnected')], netsplit: ['warn', t('health.kindNetsplit')] })
  const method = $derived(t(METHODS.find((m) => m[0] === net.authMethod)?.[1] || 'services.method.auto'))
  let table = $state(false)
</script>

{#if !data}
  <div class="skel" style="height:120px"></div><div class="skel" style="height:320px;margin-top:16px"></div>
{:else}
  <div class="range-row enter">
    <div class="chips" role="group" aria-label={t('health.range')}>
      {#each RANGES as r}<button class="chip-btn" type="button" aria-pressed={range === r} onclick={() => onrange(r)}>{rangeName[r]}</button>{/each}
    </div>
    {#if loading}<span class="faint" style="font-size:var(--fs-xs)" role="status">{t('health.loading')}</span>{/if}
  </div>

  {#if !metrics}
    <div class="callout enter" role="status" data-testid="health-no-metrics"><Icon name="chart" /><span><b>{t('health.noMetrics')}</b> {t('health.noMetricsHelp')}{#if data.reason}{' '}<span class="faint">({data.reason})</span>{/if}</span></div>
  {/if}

  <div class="tiles health-tiles enter" aria-busy={loading}>
    <div class="tile" data-testid="tile-status">
      <div class="tile-top"><span class="caps"><Icon name="plug" class="i-sm" />{t('health.status')}</span><StatusChip status={live.status} /></div>
      <div class="od-stat"><span class="n">{connected && live.connectedAt ? since(live.connectedAt) : '—'}</span><span class="u od-truncate">{connected ? (live.server ? t('health.upOn', { server: live.server }) : t('health.up')) : !net.enabled ? t('health.switchedOff') : live.lastError || t('health.notConnected')}</span></div>
    </div>
    <div class="tile" data-testid="tile-uptime">
      <div class="tile-top"><span class="caps"><Icon name="check" class="i-sm" />{t('health.uptime')}</span></div>
      <div class="od-stat"><span class="n">{metrics && sum.uptimePct != null ? sum.uptimePct + '%' : '—'}</span><span class="u">{metrics ? over : t('health.needsMetrics')}</span></div>
      <span class="ubar {uptimeTone(sum.uptimePct) === 'ok' ? '' : uptimeTone(sum.uptimePct)}" role="img" aria-label={t('health.uptimeAria', { pct: sum.uptimePct ?? 0 })}><span style="transform:scaleX({(sum.uptimePct || 0) / 100})"></span></span>
    </div>
    <div class="tile" data-testid="tile-lag">
      <div class="tile-top"><span class="caps"><Icon name="activity" class="i-sm" />{t('health.lagNow')}</span>{#if lagTone(live.lag) === 'warn' || lagTone(live.lag) === 'bad'}<span class="chip {lagTone(live.lag)}">{t('health.slow')}</span>{/if}</div>
      <div class="od-stat"><span class="n">{live.lag != null ? ms(live.lag) : '—'}</span><span class="u">{metrics && sum.lagAvg != null ? t('health.lagAvgPeak', { avg: ms(sum.lagAvg), peak: ms(sum.lagPeak) }) : live.lag != null ? t('health.lagCaps') : connected ? t('health.measuring') : t('health.noLink')}</span></div>
    </div>
    <div class="tile" data-testid="tile-messages">
      <div class="tile-top"><span class="caps"><Icon name="message" class="i-sm" />{t('health.messages')}</span></div>
      <div class="od-stat"><span class="n">{metrics ? kfmt(sum.lines) : '—'}</span><span class="u">{metrics ? t('health.linesOver', { over }) : t('health.needsMetrics')}</span></div>
    </div>
    <div class="tile" data-testid="tile-disconnects">
      <div class="tile-top"><span class="caps"><Icon name="alert" class="i-sm" />{t('health.disconnects')}</span></div>
      <div class="od-stat"><span class="n">{metrics ? kfmt(disconnects) : '—'}</span><span class="u">{metrics ? t('health.droppedFailed', { dropped: sum.dropped, failed: sum.failed }) : t('health.needsMetrics')}</span></div>
    </div>
  </div>

  {#if live.lastError && !connected}
    <p class="callout warn enter" data-testid="health-error"><Icon name="alert" /><span><b>{cap(live.lastError)}</b>{#if live.lastErrorAt}{' '}<span class="faint">({ago(live.lastErrorAt)})</span>{/if}.{#if live.hint}{' '}{live.hint}{/if}{#if live.retryIn}{' '}{t('health.retryIn', { wait: wait(live.retryIn) })}{/if}{#if live.attempts > 1}{' '}{t('health.attempts', { count: live.attempts })}{/if}{#if logHref}{' '}<a href={logHref}>{t('health.connectionLog')}</a>{/if}</span></p>
  {/if}

  <div class="grid-main-side">
    <div class="charts" aria-busy={loading}>
      {#if !metrics}
        <section class="card"><div class="empty"><span class="ic"><Icon name="chart" /></span><h3>{t('health.noCharts')}</h3><p>{t('health.noChartsHelp')}</p></div></section>
      {:else}
        <section class="card" aria-labelledby="h-uptime">
          <div class="section-head"><div class="od-stack"><span class="caps">{over}</span><h2 id="h-uptime">{t('health.uptimeTitle')}</h2></div>{#if sum.uptimePct != null}<span class="chip {uptimeTone(sum.uptimePct)}">{t('health.uptimePct', { pct: sum.uptimePct })}</span>{/if}</div>
          <UptimeBand points={se.uptime} label={slotLabel} {axis} />
        </section>

        <section class="card" aria-labelledby="h-lag">
          <div class="section-head"><div class="od-stack"><span class="caps">{t('health.lagCaps')}</span><h2 id="h-lag">{t('health.lagTitle')}</h2></div></div>
          {#if !hasLag}<div class="chart-empty">{t('health.noLag')}</div>
          {:else}<MetricChart series={lag} start={data.start} {step} unit="ms" fancy />{/if}
        </section>

        <section class="card" aria-labelledby="h-msgs">
          <div class="section-head"><div class="od-stack"><span class="caps">{step >= 86400 ? t('health.perDay') : t('health.perHour')}</span><h2 id="h-msgs">{t('health.messagesTitle')}</h2></div><span class="chip">{t('health.total', { total: kfmt(sum.lines) })}</span></div>
          {#if !sum.lines}<div class="chart-empty">{t('health.noMessages')}</div>
          {:else}<SlotBars series={lines} label={slotLabel} {axis} fmt={kfmt} testid="messages-chart" />{/if}
        </section>

        <section class="card" aria-labelledby="h-drops">
          <div class="section-head">
            <div class="od-stack"><span class="caps">{step >= 86400 ? t('health.perDay') : t('health.perHour')}</span><h2 id="h-drops">{t('health.disconnectsTitle')}</h2></div>
            <div class="legend" data-testid="drops-legend">{#each drops as d, k}<span><i class="sq" style="--c:{d.color}"></i>{d.name}<b>{k ? sum.failed : sum.dropped}</b></span>{/each}</div>
          </div>
          {#if !disconnects}<div class="chart-empty">{t('health.noDisconnects')}</div>
          {:else}<SlotBars series={drops} label={slotLabel} {axis} testid="disconnects-chart" />{/if}
          <p class="desc" style="font-size:var(--fs-xs);margin-top:var(--s2)">{t('health.disconnectsHelp')}</p>
        </section>

        <section class="card tight" aria-labelledby="h-table">
          <div class="section-head">
            <h2 id="h-table" class="caps">{t('health.tableTitle')}</h2>
            <button class="btn btn-ghost btn-sm" type="button" aria-expanded={table} aria-controls="health-table" onclick={() => (table = !table)}><Icon name={table ? 'chevronDown' : 'chevron'} class="i-sm" />{table ? t('health.hideTable') : t('health.showTable')}</button>
          </div>
          {#if table}
            <div class="table-wrap slot-table" id="health-table">
              <table>
                <thead><tr><th scope="col">{t('health.colWhen')}</th><th scope="col">{t('health.uptime')}</th><th scope="col">{t('health.lagAvg')}</th><th scope="col">{t('health.lagWorst')}</th><th scope="col">{t('health.messages')}</th><th scope="col">{t('health.dropped')}</th><th scope="col">{t('health.failed')}</th></tr></thead>
                <tbody>
                  {#each Array(n) as _, i}
                    {@const k = n - 1 - i}
                    <tr><th scope="row">{slotLabel(k)}</th><td>{pct(se.uptime[k])}</td><td>{ms(se.lagAvg[k])}</td><td>{ms(se.lagMax[k])}</td><td>{se.lines[k]}</td><td>{se.dropped[k]}</td><td>{se.failed[k]}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </section>
      {/if}
    </div>

    <div class="side">
      <section class="card tight" aria-labelledby="h-info">
        <div class="section-head" style="margin-bottom:var(--s3)"><h2 id="h-info" class="caps">{t('health.info')}</h2></div>
        <div class="kv-list" data-testid="health-info">
          <div class="kv"><div class="od-field"><span class="k">{t('health.server')}</span><span class="v">{live.server || `${net.host}:${net.port}`}{net.servers > 1 ? ' · ' + t('health.nServers', { count: net.servers }) : ''}</span></div></div>
          <div class="kv"><div class="od-field"><span class="k">{t('health.tls')}</span><span class="v">{net.tls ? t('health.tlsOn') : t('health.tlsOff')}</span></div>{#if !net.tls}<span class="chip warn">{t('health.plaintext')}</span>{/if}</div>
          <div class="kv"><div class="od-field"><span class="k">{t('health.nick')}</span><span class="v">{live.nick || net.nick}{live.nick && net.nick && live.nick !== net.nick ? ' · ' + t('health.wanted', { nick: net.nick }) : ''}</span></div></div>
          <div class="kv"><div class="od-field"><span class="k">{t('health.ident')}</span><span class="v">{net.username}</span></div></div>
          {#if net.realname}<div class="kv"><div class="od-field"><span class="k">{t('health.realname')}</span><span class="v">{net.realname}</span></div></div>{/if}
          <div class="kv"><div class="od-field"><span class="k">{t('health.auth')}</span><span class="v">{method}</span></div>{#if authLabel(live.auth, t)}<span class="chip {live.auth === 'identified' ? 'ok' : live.auth === 'failed' ? 'bad' : ''}">{authLabel(live.auth, t)}</span>{/if}</div>
          <div class="kv"><div class="od-field"><span class="k">{t('health.connectedSince')}</span><span class="v">{connected && live.connectedAt ? `${date(live.connectedAt)} ${clock(live.connectedAt)}` : '—'}</span></div></div>
        </div>
      </section>

      <section class="card tight" aria-labelledby="h-events">
        <div class="section-head" style="margin-bottom:var(--s2)">
          <h2 id="h-events" class="caps">{t('health.events')}</h2>
          {#if logHref}<a class="btn btn-ghost btn-sm" href={logHref} data-testid="health-log-link"><Icon name="terminal" class="i-sm" />{t('health.connectionLog')}</a>{/if}
        </div>
        <div class="event-list" data-testid="health-events">
          {#each data.events as e}
            {@const k = KIND[e.kind] || ['', e.kind]}
            <div class="event"><time title={`${date(e.at)} ${clock(e.at)}`}>{n > 24 || step >= 86400 ? date(e.at) : clock(e.at)}</time><div class="od-field"><span><span class="chip {k[0]}" style="margin-right:6px">{k[1]}</span>{e.title}</span>{#if e.detail}<span>{e.detail}</span>{/if}</div></div>
          {:else}
            <p class="desc" style="font-size:var(--fs-xs)">{t('health.noEvents')}</p>
          {/each}
        </div>
      </section>

      <section class="card tight" aria-labelledby="h-chans">
        <div class="section-head" style="margin-bottom:var(--s2)"><h2 id="h-chans" class="caps">{t('health.channels')}</h2><span class="chip">{data.channels.length}</span></div>
        {#if data.channels.length}
          <div class="table-wrap">
            <table class="chan-table" data-testid="health-channels">
              <thead><tr><th scope="col">{t('health.colChannel')}</th><th scope="col" class="num">{t('health.colMembers')}</th><th scope="col" class="num">{t('health.colUnread')}</th></tr></thead>
              <tbody>
                {#each data.channels as c (c.name)}
                  <tr>
                    <th scope="row">{#if chatHref}<a href={chatHref(c.name)}>{c.name}</a>{:else}{c.name}{/if}</th>
                    <td class="num">{c.members != null ? c.members.toLocaleString(i18n.intl) : '—'}</td>
                    <td class="num">{#if c.mentions}<span class="count mention" aria-label={t('health.mentionsAria', { count: c.mentions })}>{c.mentions}</span>{/if}{#if c.unread}<span class="count" aria-label={t('health.unreadAria', { count: c.unread })}>{c.unread}</span>{:else if !c.mentions}<span class="faint">0</span>{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}
          <p class="desc" style="font-size:var(--fs-xs)">{connected ? t('health.noChannelsJoined') : t('health.noChannels')}</p>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .range-row { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; margin-bottom: var(--s5); }
  .health-tiles { grid-template-columns: repeat(5, minmax(0, 1fr)); margin-bottom: var(--s5); }
  .charts, .side { display: grid; gap: var(--s5); min-width: 0; }
  .charts .chart-empty { height: 120px; }
  .legend .sq { width: 10px; height: 10px; border-radius: 3px; }
  .legend b { font-family: var(--font-mono); font-weight: 500; color: var(--fg); }
  .slot-table { max-height: 420px; overflow-y: auto; }
  table th[scope="row"] { font-size: var(--fs-sm); letter-spacing: 0; text-transform: none; color: var(--fg); font-weight: 500; padding: var(--s2) var(--s4); background: var(--row); border-radius: var(--r-row) 0 0 var(--r-row); white-space: nowrap; }
  table td { padding: var(--s2) var(--s4); font-family: var(--font-mono); font-size: var(--fs-xs); }
  tbody tr { cursor: default; }
  .chan-table .num { text-align: right; }
  .chan-table .count + .count { margin-left: 4px; }
  @media (max-width: 1199px) { .health-tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 639px) { .health-tiles { grid-template-columns: minmax(0, 1fr); } }
</style>
