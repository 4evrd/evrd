<script>
  // A network's health: its link now and over a range (?range=24h|7d|30d).
  import { onMount, onDestroy } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import NetworkHealth from '$lib/components/NetworkHealth.svelte'
  import { api } from '$lib/api.js'
  import { rangeOf } from '$lib/health.js'
  import { t } from '$lib/i18n.svelte.js'

  const id = $derived(+page.params.id)
  const range = $derived(rangeOf(page.url.searchParams.get('range')))
  let data = $state(null)
  let err = $state('')
  let loading = $state(false)
  let timer, seq = 0
  async function load() {
    const mine = ++seq // only the latest answer counts
    loading = true
    try {
      const d = await api.networkHealth(id, range)
      if (mine === seq) { data = d; err = '' }
    } catch (e) { if (mine === seq) err = e.message }
    if (mine === seq) loading = false
  }
  // a new range (or network) reloads, keeping the old charts until it's in
  $effect(() => { id; range; load() })
  onMount(() => { timer = setInterval(load, 30000) })
  onDestroy(() => clearInterval(timer))
  const pick = (r) => goto(`${page.url.pathname}?range=${r}`, { replaceState: true, keepFocus: true, noScroll: true })
  const name = $derived(data?.network?.name)
</script>

<svelte:head><title>{name ? t('health.pageTitle', { name }) : t('health.title')} · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <a class="back" href="/networks#net-{id}"><Icon name="chevron-left" class="i-sm" />{t('health.back')}</a>
    <h1 data-testid="health-title">{name ? t('health.heading', { name }) : t('health.title')}</h1>
    <p>{data ? `${data.network.host}:${data.network.port}${data.network.tls ? ' · TLS' : ''}` : t('health.intro')}</p>
  </div>
  {#if data}
    <div class="od-cluster">
      <a class="btn btn-ghost" href="/networks/{id}"><Icon name="pencil" />{t('health.settings')}</a>
      <a class="btn" href="/chat?net={id}"><Icon name="message" />{t('health.openChat')}</a>
    </div>
  {/if}
</div>

{#if err && !data}
  <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('health.loadFailed', { error: err })} <a href="/networks">{t('health.back')}</a></span></div>
{:else}
  <NetworkHealth {data} {range} onrange={pick} {loading} logHref="/networks/{id}#log" chatHref={(c) => `/chat?net=${id}&buf=${encodeURIComponent(c)}`} />
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--muted); }
</style>
