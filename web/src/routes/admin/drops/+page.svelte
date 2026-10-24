<script>
  // Why IRC clients disconnect, across every account: by reason, how long
  // devices were gone, and the devices that drop most. Telling phones that
  // lose signal from slow or congested links (issue #92).
  import { onMount } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import Select from '$lib/components/Select.svelte'
  import SlotBars from '$lib/components/SlotBars.svelte'
  import { slotTimes, ticks } from '$lib/health.js'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { dur, ago, clock, date } from '$lib/format.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  const RANGES = $derived([['24h', t('admin.drops.range24h')], ['7d', t('admin.drops.range7d')], ['30d', t('admin.drops.range30d')]])
  let range = $state('24h')
  let sort = $state('drops')
  let d = $state(null)
  let err = $state('')
  let loading = $state(false)
  async function load() {
    loading = true
    try { d = await api.admin.clientDrops(range, sort); err = '' } catch (e) { err = e.message }
    loading = false
  }
  function pickRange(r) { range = r; load() }
  function pickSort(s) { sort = s; load() }
  onMount(() => { if (isAdmin) load() })

  // Each reason keeps its colour whatever else is shown (identity, not rank).
  const COLOR = {
    ping_timeout: 'var(--signal)', reset: 'var(--bad)', write_stalled: 'var(--warn)', closed: 'var(--nick)',
    quit: 'var(--ok)', replaced: 'var(--cyan)', client_limit: 'var(--faint)', evrd_stopping: 'var(--faint)', removed: 'var(--faint)',
  }
  // Every reason, always, in the server's order: what didn't happen tells
  // as much as what did. seen is the ones that did, for the gap legend.
  const reasons = $derived(d ? d.order : [])
  const seen = $derived(reasons.filter((r) => d.reasons[r]))
  // Over time, as the network health page draws it: a column per slot,
  // stacked by the reasons that happened.
  const n = $derived(d?.points || 0)
  const step = $derived(d?.step || 3600)
  const overTime = $derived(seen.map((r) => ({ name: t('clients.reason.' + r), color: COLOR[r], points: d.timeline[r] })))
  const weekday = (u) => new Date(u * 1000).toLocaleDateString(i18n.intl, { weekday: 'short' })
  function slotLabel(i) {
    const s = slotTimes(d.start, step, i, n)
    if (step >= 86400) return date(s.from + step / 2) + (s.current ? ' · ' + t('health.soFar') : '')
    const day = n > 24 ? weekday(s.from) + ' ' : ''
    return day + (s.current ? t('format.untilNow', { from: clock(s.from) }) : `${clock(s.from)}–${clock(s.to)}`)
  }
  const axis = $derived(d ? ticks(n, step).map((i) => [i, i === n ? t('format.now') : step >= 86400 ? date(d.start + i * step + step / 2) : n > 24 ? weekday(d.start + i * step) : clock(d.start + i * step)]) : [])
  const most = $derived(Math.max(1, ...reasons.map((r) => d.reasons[r] || 0)))
  const sortOptions = $derived([{ value: 'drops', label: t('admin.drops.sortDrops') }, ...reasons.map((r) => ({ value: r, label: t('clients.reason.' + r) }))])
  const buckets = $derived(d ? d.buckets.map((b, i) => ({ label: bucketLabel(i), counts: b, total: Object.values(b).reduce((a, n) => a + n, 0) })) : [])
  const bucketMax = $derived(Math.max(1, ...buckets.map((b) => b.total)))
  function bucketLabel(i) {
    const bounds = d.bucketBounds
    if (i < bounds.length) return t('admin.drops.within', { gap: dur(bounds[i]) })
    if (i === bounds.length) return t('admin.drops.longer', { gap: dur(bounds[bounds.length - 1]) })
    return t('admin.drops.notBack')
  }
  const pct = (n, of) => `${Math.max(0, Math.min(100, (n / of) * 100))}%`
</script>

<svelte:head><title>{t('admin.drops.pageTitle')}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('admin.drops.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('admin.drops.heading')}</h1>
      <p>{t('admin.drops.intro')}</p>
    </div>
    <div class="tabs status-tabs" role="tablist" aria-label={t('admin.drops.range')}>
      {#each RANGES as [k, l]}<button class="tab" role="tab" aria-selected={range === k} onclick={() => pickRange(k)}>{l}</button>{/each}
    </div>
  </div>

  {#if err && !d}
    <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('admin.drops.loadFailed', { error: err })}</span></div>
  {:else if !d}
    <div class="card"><div class="skel" style="height:220px"></div></div>
  {:else if !d.drops}
    <div class="empty enter" data-testid="drops-empty"><span class="ic"><Icon name="check" /></span><h3>{t('admin.drops.none')}</h3><p>{t('admin.drops.noneHelp')}</p></div>
  {:else}
    <div class="drops-grid enter" aria-busy={loading}>
      <section class="card" aria-labelledby="dr-why" data-testid="drops-reasons">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <div class="od-stack"><span class="caps">{t('admin.drops.summary', { drops: d.drops, accounts: d.accounts, devices: d.devices })}</span><h2 id="dr-why">{t('admin.drops.why')}</h2></div>
        </div>
        <ul class="reasons">
          {#each reasons as r (r)}
            {@const g = d.gaps[r] || {}}
            <li data-testid="reason-{r}" class:none={!d.reasons[r]}>
              <div class="rtop"><span class="rname"><i style="background:{COLOR[r]}" aria-hidden="true"></i>{t('clients.reason.' + r)}</span><b class="mono">{d.reasons[r] || 0}</b></div>
              <span class="rbar" aria-hidden="true"><span style="width:{pct(d.reasons[r] || 0, most)};background:{COLOR[r]}"></span></span>
              <p class="desc rmeans">{t('admin.drops.means.' + r)}</p>
              <p class="sub">{g.back ? t('admin.drops.backAfter', { median: dur(Math.round(g.median)), p90: dur(Math.round(g.p90)) }) : ''}{g.back && g.gone ? ' · ' : ''}{g.gone ? t('admin.drops.notBackN', { count: g.gone }) : ''}</p>
            </li>
          {/each}
        </ul>
      </section>

      <section class="card" aria-labelledby="dr-gap" data-testid="drops-gaps">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <div class="od-stack"><span class="caps">{t('admin.drops.gapCaps')}</span><h2 id="dr-gap">{t('admin.drops.gapTitle')}</h2></div>
        </div>
        <p class="desc" style="font-size:var(--fs-xs);margin-bottom:var(--s3)">{t('admin.drops.gapHelp')}</p>
        <table class="gaps">
          <caption class="sr-only">{t('admin.drops.gapTitle')}</caption>
          <tbody>
            {#each buckets as b, i (i)}
              <tr>
                <th scope="row">{b.label}</th>
                <td class="gbar">
                  <span class="stack" style="width:{pct(b.total, bucketMax)}">
                    {#each reasons as r}{#if b.counts[r]}<i style="flex:{b.counts[r]};background:{COLOR[r]}" title="{t('clients.reason.' + r)}: {b.counts[r]}"></i>{/if}{/each}
                  </span>
                </td>
                <td class="mono num">{b.total}</td>
              </tr>
            {/each}
          </tbody>
        </table>
        <div class="legend" style="margin-top:var(--s3)">
          {#each seen as r}<span><i style="--c:{COLOR[r]}"></i>{t('clients.reason.' + r)}</span>{/each}
        </div>
      </section>

      <section class="card wide" aria-labelledby="dr-time" data-testid="drops-time">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <div class="od-stack"><span class="caps">{t('admin.drops.timeCaps')}</span><h2 id="dr-time">{t('admin.drops.timeTitle')}</h2></div>
        </div>
        <SlotBars series={overTime} label={slotLabel} {axis} testid="drops-chart" />
        <div class="legend" style="margin-top:var(--s3)">
          {#each seen as r}<span><i style="--c:{COLOR[r]}"></i>{t('clients.reason.' + r)}</span>{/each}
        </div>
        <p class="desc" style="font-size:var(--fs-xs);margin-top:var(--s2)">{step >= 86400 ? t('admin.drops.timeHelpDay') : t('admin.drops.timeHelpHour')}</p>
      </section>

      <section class="card wide" aria-labelledby="dr-top" data-testid="drops-top">
        <div class="section-head" style="margin-bottom:var(--s3)">
          <div class="od-stack"><span class="caps">{t('admin.drops.topCaps')}</span><h2 id="dr-top">{t('admin.drops.topTitle')}</h2></div>
          <div class="sortby"><label class="sub" for="drops-sort">{t('admin.drops.sortBy')}</label>
            <Select id="drops-sort" testid="drops-sort" value={sort} options={sortOptions} onchange={pickSort} />
          </div>
        </div>
        <div class="table-wrap">
          <table class="top">
            <thead><tr><th scope="col">{t('admin.drops.colUser')}</th><th scope="col">{t('admin.drops.colDevice')}</th><th scope="col">{t('admin.drops.colReason')}</th><th scope="col" class="num">{t('admin.drops.colDrops')}</th><th scope="col">{t('admin.drops.colBack')}</th><th scope="col">{t('admin.drops.colLast')}</th></tr></thead>
            <tbody>
              {#each d.top as x (x.userId + '/' + x.device + '/' + x.software + '/' + x.network)}
                <tr data-testid="drops-row">
                  <td><a href="/admin/users/{x.userId}">{x.user}</a></td>
                  <td><span class="od-field"><span>{x.device || x.software || t('clients.ircClient')}{x.device && x.software ? ' · ' + x.software : ''}</span><span class="sub">{x.network}</span></span>{#if x.connected}<span class="chip ok" style="margin-left:var(--s2)">{t('admin.drops.connected')}</span>{/if}</td>
                  <td><span class="rname"><i style="background:{COLOR[x.mainReason]}" aria-hidden="true"></i>{t('clients.reason.' + x.mainReason)}</span></td>
                  <td class="mono num">{sort === 'drops' ? x.drops : `${x.reasons[sort] || 0} / ${x.drops}`}</td>
                  <td class="mono">{x.medianGap ? dur(Math.round(x.medianGap)) : '—'}</td>
                  <td class="sub">{ago(x.last)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if d.top.some((x) => !x.device)}<p class="desc" style="font-size:var(--fs-xs);margin-top:var(--s3)">{t('clients.dropsHint')}</p>{/if}
      </section>
    </div>
  {/if}
{/if}

<style>
  .drops-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 420px), 1fr)); gap: var(--s5); align-items: start; }
  .drops-grid .wide { grid-column: 1 / -1; min-width: 0; }
  .reasons { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--s4); }
  .rtop { display: flex; justify-content: space-between; align-items: baseline; gap: var(--s3); }
  .rname { display: inline-flex; align-items: center; gap: 8px; color: var(--fg); }
  .rname i { width: 10px; height: 10px; border-radius: 3px; flex: none; }
  .rbar { display: block; height: 6px; border-radius: 3px; background: var(--row); margin: 6px 0; overflow: hidden; }
  .rbar span { display: block; height: 100%; border-radius: 3px; }
  .rmeans { font-size: var(--fs-xs); margin: 0; }
  .reasons .sub { font-size: var(--fs-xs); margin: 2px 0 0; }
  .gaps { width: 100%; border-collapse: separate; border-spacing: 0 6px; }
  .gaps th { font-size: var(--fs-xs); font-weight: 400; color: var(--muted); text-align: left; white-space: nowrap; padding-right: var(--s3); width: 1%; }
  .gaps .gbar { width: 100%; }
  .gaps .stack { display: flex; gap: 2px; height: 14px; min-width: 2px; }
  .gaps .stack i { border-radius: 3px; min-width: 2px; }
  .num { text-align: right; }
  .mono { font-family: var(--font-mono); font-size: var(--fs-sm); }
  .sortby { display: inline-flex; align-items: center; gap: var(--s2); }
  .sortby :global(.dd) { min-width: 200px; }
  .reasons li.none { opacity: 0.55; }
  .table-wrap { overflow-x: auto; }
  .top { width: 100%; border-collapse: collapse; font-size: var(--fs-sm); }
  .top th { font-size: var(--fs-xs); color: var(--muted); font-weight: 500; text-align: left; padding: var(--s2) var(--s3); white-space: nowrap; }
  .top td { padding: var(--s2) var(--s3); border-top: 1px solid var(--line-soft); vertical-align: middle; }
  .top td .od-field { display: inline-flex; }
  @media (prefers-reduced-motion: no-preference) { .rbar span, .gaps .stack { transition: width .25s ease; } }
</style>
