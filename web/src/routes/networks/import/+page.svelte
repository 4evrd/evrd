<script>
  import Icon from '$lib/components/Icon.svelte'
  import NetworkImport from '$lib/components/NetworkImport.svelte'
  import { api } from '$lib/api.js'
  import { refreshNets, refreshMe } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'
</script>

<svelte:head><title>{t('transfer.title')} · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <a class="back" href="/networks"><Icon name="chevron-left" class="i-sm" />{t('networks.title')}</a>
    <h1>{t('transfer.title')}</h1>
    <p>{t('transfer.intro')}</p>
  </div>
</div>

<NetworkImport preview={api.previewImport} run={api.importNetworks} done="/networks" doneLabel={t('transfer.toNetworks')}
  ondone={() => { refreshNets(); refreshMe() }} />

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: var(--fs-sm); }
</style>
