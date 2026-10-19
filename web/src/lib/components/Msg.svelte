<script>
  // A translated sentence with markup inside: {name}s that match a snippet
  // prop render it, the others are filled from vars. The markup stays here,
  // the words in the message files:
  //   <Msg key="clients.useClientPassword" vars={{ label }}>
  //     {#snippet format()}<span class="code">{fmt}</span>{/snippet}
  //   </Msg>
  import { entry } from '$lib/i18n.svelte.js'

  let { key, vars = {}, ...snippets } = $props()
  const parts = $derived(entry(key, vars).split(/(\{\w+\})/).filter(Boolean))
</script>

{#each parts as p}{#if /^\{\w+\}$/.test(p) && snippets[p.slice(1, -1)]}{@render snippets[p.slice(1, -1)]()}{:else if /^\{\w+\}$/.test(p) && p.slice(1, -1) in vars}{vars[p.slice(1, -1)]}{:else}{p}{/if}{/each}
