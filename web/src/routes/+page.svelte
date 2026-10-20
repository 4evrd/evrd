<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import UserOverview from '$lib/components/UserOverview.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { greeting } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  let ov = $state(null)
  let err = $state('')
  let timer
  async function load() {
    try { ov = await api.overview(); err = '' } catch (e) { err = e.message }
  }
  onMount(() => { load(); timer = setInterval(load, 15000) })
  onDestroy(() => clearInterval(timer))

  const me = $derived(session.me)
  const nets = $derived(ov?.networks || [])
  const connected = $derived(nets.filter((n) => n.status === 'connected').length)
  const clients = $derived(ov?.clients || [])
</script>

<svelte:head><title>{t('overview.pageTitle')}</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>{t('overview.title')}</h1>
    <p>{t('overview.hello', { greeting: greeting(), name: me.username })} {#if ov}{t('overview.summary', { networks: t('overview.summaryNetworks', { connected, count: nets.length }), clients: t('overview.summaryClients', { count: clients.length }) })}{/if}</p>
  </div>
  <div class="od-cluster">
    <a class="btn" href="/clients"><Icon name="plug" />{t('overview.connectClient')}</a>
    <a class="btn btn-primary" href="/networks/new"><Icon name="plus" />{t('overview.addNetwork')}</a>
  </div>
</div>

{#if err && !ov}
  <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('overview.loadFailed', { error: err })}</span></div>
{/if}

<UserOverview {ov} usage={me.usage} limits={me.limits} />
