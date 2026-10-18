<script>
  // A side drawer: open when drawers.open === id. Esc and the overlay close it.
  import { tick } from 'svelte'
  import Icon from './Icon.svelte'
  import { drawers, closeDrawers } from '$lib/ui.svelte.js'

  let { id, title, sub = '', head = null, children, foot = null } = $props()
  let el = $state()
  const open = $derived(drawers.open === id)
  $effect(() => {
    if (open && el) tick().then(() => setTimeout(() => (el.querySelector('[autofocus]') || el.querySelector('input:not([readonly]),select,textarea,button:not([data-close])'))?.focus(), 60))
  })
</script>

<svelte:window onkeydown={(e) => { if (e.key === 'Escape' && open) closeDrawers() }} />

<div class="overlay" class:show={open} onclick={closeDrawers} aria-hidden="true"></div>
<div class="drawer" class:open {id} role="dialog" aria-modal="true" aria-labelledby="{id}-title" inert={!open} bind:this={el}>
  <div class="drawer-head">
    {#if head}{@render head()}{:else}
      <div class="od-stack"><h2 id="{id}-title">{title}</h2>{#if sub}<p>{sub}</p>{/if}</div>
    {/if}
    <button class="icon-btn" type="button" data-close aria-label="Close" onclick={closeDrawers}><Icon name="x" /></button>
  </div>
  {@render children()}
  {#if foot}<div class="drawer-foot">{@render foot()}</div>{/if}
</div>
