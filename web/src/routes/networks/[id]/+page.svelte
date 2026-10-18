<script>
  // The chat used to live at /networks/ID; it's /chat?net=ID now.
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  onMount(() => goto(`/chat?net=${page.params.id}`, { replaceState: true }))
</script>
