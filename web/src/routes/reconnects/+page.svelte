<script>
  // Which networks reconnected in the last day, and why.
  import { onMount, onDestroy } from 'svelte'
  import Reconnects from '$lib/components/Reconnects.svelte'
  import { api } from '$lib/api.js'
  import { toast } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let data = $state(null)
  let timer
  async function load() {
    try { data = await api.reconnects() } catch (e) { if (!data) toast(e.message, 'bad') }
  }
  onMount(() => { load(); timer = setInterval(load, 15000) })
  onDestroy(() => clearInterval(timer))
</script>

<svelte:head><title>{t('overview.rcPageTitle')}</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>{t('overview.rcTitle')}</h1>
    <p>{t('overview.rcIntro')}</p>
  </div>
</div>

<section class="card enter" aria-label={t('overview.rcByNetwork')}>
  <Reconnects {data} logHref={(n) => `/networks/${n.id}#log`} />
</section>
