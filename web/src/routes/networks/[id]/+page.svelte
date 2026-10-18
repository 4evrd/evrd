<script>
  import { page } from '$app/state'
  import NetworkEditor from '$lib/components/NetworkEditor.svelte'
</script>

{#key page.params.id}<NetworkEditor id={+page.params.id} />{/key}
