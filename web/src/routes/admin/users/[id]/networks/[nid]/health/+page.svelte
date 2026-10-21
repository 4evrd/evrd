<script>
  // A user's network's health, for admins: the same page as the user's own.
  import { onMount, onDestroy } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  import Icon from '$lib/components/Icon.svelte'
  import NetworkHealth from '$lib/components/NetworkHealth.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { rangeOf } from '$lib/health.js'
  import { t } from '$lib/i18n.svelte.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  const uid = $derived(+page.params.id)
  const nid = $derived(+page.params.nid)
  const range = $derived(rangeOf(page.url.searchParams.get('range')))
  let data = $state(null)
  let err = $state('')
  let loading = $state(false)
  let timer, seq = 0
  async function load() {
    const mine = ++seq // only the latest answer counts
    loading = true
    try {
      const d = await api.admin.networkHealth(uid, nid, range)
      if (mine === seq) { data = d; err = '' }
    } catch (e) { if (mine === seq) err = e.message }
    if (mine === seq) loading = false
  }
  $effect(() => { uid; nid; range; if (isAdmin) load() })
  onMount(() => { timer = setInterval(() => isAdmin && load(), 30000) })
  onDestroy(() => clearInterval(timer))
  const pick = (r) => goto(`${page.url.pathname}?range=${r}`, { replaceState: true, keepFocus: true, noScroll: true })
  const name = $derived(data?.network?.name)
</script>

<svelte:head><title>{name ? t('health.pageTitle', { name }) : t('health.title')} · evrd</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('adminUsers.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/admin/users/{uid}#net-{nid}"><Icon name="chevron-left" class="i-sm" />{t('health.backToUser')}</a>
      <h1 data-testid="health-title">{name ? t('health.heading', { name }) : t('health.title')}</h1>
      <p>{data ? `${data.network.host}:${data.network.port}${data.network.tls ? ' · TLS' : ''}` : t('health.intro')}</p>
    </div>
  </div>
  {#if err && !data}
    <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('health.loadFailed', { error: err })}</span></div>
  {:else}
    <NetworkHealth {data} {range} onrange={pick} {loading} logHref="/admin/users/{uid}#net-{nid}" />
  {/if}
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; font-size: var(--fs-sm); color: var(--muted); }
</style>
