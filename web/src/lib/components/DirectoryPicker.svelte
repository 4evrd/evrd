<script>
  // Pick a public network from the directory (GET /api/directory) to fill
  // in the add-network form. Shows nothing when the server has no directory.
  import Icon from './Icon.svelte'
  import { api } from '$lib/api.js'
  import Msg from './Msg.svelte'
  import { searchNetworks, connectServers, serverLabel } from '$lib/directory.js'
  import { t } from '$lib/i18n.svelte.js'

  let { picked = null, onpick, onmanual } = $props()

  let nets = $state(null)
  let q = $state('')
  api.directory().then((d) => (nets = d.networks || [])).catch(() => (nets = []))
  const shown = $derived(searchNetworks(nets, q))

  // Enter picks the first match rather than submitting the form.
  function key(e) {
    if (e.key !== 'Enter') return
    e.preventDefault()
    if (shown.length) onpick(shown[0])
  }
</script>

{#if nets?.length}
  <section class="card" id="directory" aria-labelledby="directory-h" data-testid="directory">
    <div class="section-head">
      <div class="od-stack" style="--od-gap:6px">
        <h2 id="directory-h">{t('networks.dir.title')}</h2>
        <p class="desc">{t('networks.dir.intro')}</p>
      </div>
      <button class="btn btn-ghost btn-sm" type="button" onclick={onmanual}><Icon name="pencil" />{t('networks.dir.manual')}</button>
    </div>
    <label class="search dir-search">
      <Icon name="search" /><span class="sr-only">{t('networks.search')}</span>
      <input id="dir-search" type="search" placeholder={t('networks.dir.search', { count: nets.length })} autocomplete="off" spellcheck="false" bind:value={q} onkeydown={key} />
    </label>
    <div class="dir-list" role="list" aria-label={t('networks.dir.list')}>
      {#each shown as n (n.id)}
        <div role="listitem">
          <button class="dir-item" type="button" aria-pressed={picked === n.id} data-testid="directory-network" onclick={() => onpick(n)}>
            <span class="od-field">
              <span class="name">{n.name}{#if picked === n.id}<Icon name="check" class="i-sm" />{/if}</span>
              {#if n.description}<span class="sub">{n.description}</span>{/if}
            </span>
            <span class="meta mono">{serverLabel(connectServers(n)[0])}</span>
            {#if n.region || n.tags?.length}
              <span class="tags">{#each [n.region, ...(n.tags || [])].filter(Boolean) as tag}<span class="chip">{tag}</span>{/each}</span>
            {/if}
          </button>
        </div>
      {:else}
        <p class="desc"><Msg key="networks.dir.noMatch" vars={{ query: q }}>{#snippet manual()}<button class="link" type="button" onclick={onmanual}>{t('networks.dir.manualLink')}</button>{/snippet}</Msg></p>
      {/each}
    </div>
  </section>
{/if}

<style>
  .section-head { display: flex; justify-content: space-between; align-items: start; gap: var(--s4); flex-wrap: wrap; margin-bottom: var(--s4); }
  .dir-search { width: 100%; margin-bottom: var(--s3); }
  .dir-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: var(--s2); max-height: 420px; overflow-y: auto; padding: 2px; }
  .dir-item { display: grid; gap: 6px; width: 100%; height: 100%; text-align: left; padding: var(--s3) var(--s4); border-radius: var(--r-row); background: var(--row); border: 1px solid transparent; color: var(--fg); cursor: pointer; transition: background var(--t-fast) var(--ease-out), border-color var(--t-fast) var(--ease-out); }
  .dir-item:hover { background: var(--row-hover); }
  .dir-item:focus-visible { outline: none; border-color: var(--primary-dim); box-shadow: 0 0 0 3px var(--primary-soft); }
  .dir-item[aria-pressed='true'] { border-color: var(--primary-dim); background: var(--primary-soft); }
  .dir-item .name { display: inline-flex; align-items: center; gap: 6px; font-weight: 500; font-size: var(--fs-sm); }
  .dir-item .sub { font-size: var(--fs-xs); color: var(--muted); display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .dir-item .meta { font-size: var(--fs-xs); color: var(--faint); }
  .tags { display: flex; flex-wrap: wrap; gap: 4px; }
  .tags .chip { height: 20px; padding: 0 8px; font-size: var(--fs-2xs); }
  .link { background: none; border: 0; padding: 0; color: var(--primary); cursor: pointer; font: inherit; text-decoration: underline; }
</style>
