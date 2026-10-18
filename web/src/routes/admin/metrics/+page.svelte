<script>
  // Every metric evrd exports, across all users and nodes, from the metrics
  // store: one chart per area, plus the busiest networks.
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import MetricChart from '$lib/components/MetricChart.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { kfmt } from '$lib/format.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  const RANGES = [['6h', '6 hours'], ['24h', '24 hours'], ['7d', '7 days']]
  let range = $state('24h')
  let m = $state(null)
  let err = $state('')
  let loading = $state(false)
  let timer
  async function load() {
    loading = true
    try { m = await api.admin.metrics(range); err = '' } catch (e) { err = e.message }
    loading = false
  }
  function pick(r) { range = r; m = null; load() }
  onMount(() => { if (!isAdmin) return; load(); timer = setInterval(load, 60000) })
  onDestroy(() => clearInterval(timer))

  const per = $derived(m ? (m.step >= 3600 ? 'per hour' : `per ${m.step / 60} min`) : '')
  const total = (p) => p.series.reduce((a, s) => a + s.points.reduce((x, v) => x + (v || 0), 0), 0)
  const empty = (p) => !p.series.length || p.series.every((s) => s.points.every((v) => v == null))
</script>

<svelte:head><title>Metrics · evrd admin</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>Admins only.</b> Your account doesn’t have access to metrics.</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>Metrics</h1>
      <p>Everything evrd reports, across every account and node, read back from the metrics store.</p>
    </div>
    <div class="tabs status-tabs" role="tablist" aria-label="Range">
      {#each RANGES as [k, l]}<button class="tab" role="tab" aria-selected={range === k} onclick={() => pick(k)}>{l}</button>{/each}
    </div>
  </div>

  {#if err && !m}
    <div class="callout bad" role="alert"><Icon name="alert" /><span>Couldn’t load metrics: {err}</span></div>
  {:else if m && !m.available}
    <div class="empty"><span class="ic"><Icon name="chart" /></span><h3>No metrics store</h3><p>Set <span class="code">otel_endpoint</span> and <span class="code">metrics_url</span> (Admin → Settings → Metrics) to see charts here. {m.error}</p></div>
  {:else}
    {#if m?.error}<div class="callout warn" role="status"><Icon name="alert" /><span>Some charts couldn’t load: {m.error}</span></div>{/if}
    <div class="metric-grid enter" aria-busy={loading}>
      {#if !m}
        {#each Array(6) as _}<div class="card"><div class="skel" style="height:200px"></div></div>{/each}
      {:else}
        {#each m.panels as p (p.id)}
          <section class="card metric-card" aria-labelledby="mh-{p.id}" data-testid="metric-{p.id}">
            <div class="section-head" style="margin-bottom:var(--s3)">
              <div class="od-stack"><span class="caps">{p.counter ? per : 'latest per step'}</span><h2 id="mh-{p.id}">{p.title}</h2></div>
              {#if p.counter && !empty(p)}<span class="chip">{kfmt(Math.round(total(p)))} in {RANGES.find((r) => r[0] === m.range)?.[1]}</span>{/if}
            </div>
            {#if empty(p)}
              <div class="chart-empty">Nothing recorded {RANGES.find((r) => r[0] === m.range)?.[1] === '7 days' ? 'this week' : 'in this range'}.</div>
            {:else}
              <MetricChart series={p.series} start={m.start} step={m.step} unit={p.unit} />
            {/if}
            <p class="desc" style="font-size:var(--fs-xs);margin-top:var(--s2)">{p.help}</p>
          </section>
        {/each}
        <section class="card metric-card" aria-labelledby="mh-busy">
          <div class="section-head" style="margin-bottom:var(--s3)">
            <div class="od-stack"><span class="caps">Chat lines</span><h2 id="mh-busy">Busiest networks</h2></div>
          </div>
          <div class="rows">
            {#each m.busiest as b, i}
              <a class="row" href="/admin/users/{b.userId}" style="--cols:24px minmax(0,1fr) auto">
                <span class="faint mono">{i + 1}</span>
                <span class="od-field"><span class="name">{b.network}</span><span class="sub">{b.user}</span></span>
                <span class="mono">{kfmt(b.lines)}</span>
              </a>
            {:else}
              <p class="desc" style="font-size:var(--fs-sm)">No chat lines in this range.</p>
            {/each}
          </div>
        </section>
      {/if}
    </div>
  {/if}
{/if}

<style>
  .metric-grid { align-items: start; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 440px), 1fr)); gap: var(--s5); }
  .metric-card { min-width: 0; }
  .metric-card .chart-empty { height: 186px; }
  .mono { font-family: var(--font-mono); font-size: var(--fs-sm); }
</style>
