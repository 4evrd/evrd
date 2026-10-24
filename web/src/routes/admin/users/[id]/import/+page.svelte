<script>
  // An admin imports networks into one account: the same as the user's own
  // import, with an admin's say (any bind host, past the network limit).
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import NetworkImport from '$lib/components/NetworkImport.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  const id = $derived(+page.params.id)
  const isAdmin = $derived(session.me?.role === 'admin')
  let name = $state('')
  onMount(async () => {
    try { name = (await api.admin.user(id)).username } catch { /* the title waits */ }
  })
</script>

<svelte:head><title>{t('transfer.adminTitle', { name })} · evrd</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b></span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <a class="back" href="/admin/users/{id}"><Icon name="chevron-left" class="i-sm" />{name || t('adminUsers.heading')}</a>
      <h1>{t('transfer.adminTitle', { name })}</h1>
      <p>{t('transfer.adminIntro')}</p>
    </div>
  </div>
  <NetworkImport preview={(text) => api.admin.previewImport(id, text)} run={(body) => api.admin.importNetworks(id, body)}
    done="/admin/users/{id}#networks" doneLabel={t('transfer.toAccount', { name })} />
{/if}

<style>
  .back { display: inline-flex; align-items: center; gap: 4px; color: var(--muted); font-size: var(--fs-sm); }
</style>
