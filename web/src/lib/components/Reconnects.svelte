<script>
  // A user's last day of link trouble: per network, links dropped and
  // connection attempts that failed, minutes offline, the hours it happened
  // in, and why (the last error, then the events). The user's own page, and
  // an admin's view of any user.
  //
  // data:    /api/reconnects (or the admin's per-user variant)
  // logHref: (network) => where its connection log is
  import Icon from '$lib/components/Icon.svelte'
  import StatusChip from '$lib/components/StatusChip.svelte'
  import { clock, ago, since, hourSpan, wait } from '$lib/format.js'

  let { data = null, logHref = () => null } = $props()

  const nets = $derived(data?.networks || [])
  const troubled = $derived(nets.filter((n) => n.total > 0 || n.offlineMin > 0 || (n.lastError && n.status !== 'connected')))
  const fine = $derived(nets.length - troubled.length)
  const sum = (k) => nets.reduce((a, n) => a + (n[k] || 0), 0)
  const names = $derived(Object.fromEntries(nets.map((n) => [n.id, n.name])))

  let only = $state(null) // the network the events are filtered to
  const events = $derived((data?.events || []).filter((e) => !only || e.network === only))
  const KIND = { disconnected: ['bad', 'Lost'], failed: ['warn', 'Couldn’t connect'], connected: ['ok', 'Connected'], netsplit: ['warn', 'Netsplit'] }
  const peak = (n) => Math.max(1, ...(n.hourly || []))
</script>

{#if !data}
  <div class="skel" style="height:180px"></div>
{:else}
  <div class="rc-summary" data-testid="reconnects-summary">
    <div><span class="caps">Links dropped</span><span class="n">{data.available ? sum('dropped') : '—'}</span></div>
    <div><span class="caps">Failed attempts</span><span class="n">{data.available ? sum('failed') : '—'}</span></div>
    <div><span class="caps">Offline</span><span class="n">{data.available ? sum('offlineMin') + ' min' : '—'}</span></div>
  </div>
  {#if !data.available}
    <p class="callout" style="margin-top:var(--s3)"><Icon name="chart" /><span>Counts need a metrics store{data.error ? `: ${data.error}` : ''}. The events below still show what happened.</span></p>
  {/if}

  <div class="rc-list">
    {#each troubled as n (n.id)}
      {@const log = logHref(n)}
      <article class="rc-net" data-testid="reconnects-net">
        <div class="rc-top">
          <span class="rc-name"><b>{n.name}</b>
            <span class="sub">{#if data.available}{n.dropped} dropped · {n.failed} failed{#if n.total > n.dropped + n.failed} · {n.total - n.dropped - n.failed} earlier{/if} · {n.offlineMin} min offline{:else}{n.attempts || 0} attempts since evrd started{/if}</span></span>
          {#if !n.enabled}<span class="chip">Switched off</span>{/if}
          <StatusChip status={n.status} />
        </div>
        {#if data.available}
          <div class="rc-hours" role="img" aria-label="Disconnects per hour, last 24 hours">
            {#each (n.hourly || []).slice(1) as v, i}
              <span class:on={v > 0} style="height:{v ? Math.max(15, (v / peak(n)) * 100) : 8}%" title="{hourSpan(data.start, i + 1)}: {v}"></span>
            {/each}
          </div>
          <div class="rc-axis" aria-hidden="true"><span>24h ago</span><span>now</span></div>
        {/if}
        {#if n.lastError}
          <p class="callout {n.status === 'connected' ? '' : 'warn'}"><Icon name="alert" /><span><b>{n.status === 'connected' ? 'Last error' : 'Now'}: {n.lastError}</b>{#if n.lastErrorAt}{' '}<span class="faint">({ago(n.lastErrorAt)})</span>{/if}.{#if n.hint}{' '}{n.hint}{/if}{#if n.status !== 'connected' && n.retryIn}{' '}Trying again in {wait(n.retryIn)}.{/if}</span></p>
        {:else if n.status === 'connected'}
          <p class="desc" style="font-size:var(--fs-xs)">Connected now{n.connectedAt ? `, for ${since(n.connectedAt)}` : ''}{n.server ? ` to ${n.server}` : ''}.</p>
        {/if}
        <div class="rc-actions">
          <button class="btn btn-ghost btn-sm" type="button" aria-pressed={only === n.id} onclick={() => (only = only === n.id ? null : n.id)}><Icon name="sliders" class="i-sm" />{only === n.id ? 'All events' : 'Its events'}</button>
          {#if log}<a class="btn btn-ghost btn-sm" href={log}><Icon name="terminal" class="i-sm" />Connection log</a>{/if}
        </div>
      </article>
    {:else}
      <p class="desc">No reconnects in the last 24 hours.</p>
    {/each}
    {#if fine && troubled.length}<p class="desc faint" style="font-size:var(--fs-xs)">{fine} other network{fine === 1 ? '' : 's'} stayed connected.</p>{/if}
  </div>

  <div class="section-head" style="margin:var(--s5) 0 var(--s2)">
    <h3 class="caps">Events{only ? ` · ${names[only]}` : ''}</h3>
    {#if only}<button class="btn btn-ghost btn-sm" type="button" onclick={() => (only = null)}>Show all</button>{/if}
  </div>
  <div class="event-list" data-testid="reconnects-events">
    {#each events as e}
      {@const k = KIND[e.kind] || ['', e.kind]}
      <div class="event"><time>{clock(e.at)}</time><div class="od-field"><span><span class="chip {k[0]}" style="margin-right:6px">{k[1]}</span>{e.title}</span>{#if e.detail}<span>{e.detail}</span>{/if}</div></div>
    {:else}
      <p class="desc" style="font-size:var(--fs-xs)">No link events in the last 24 hours.</p>
    {/each}
  </div>
{/if}
