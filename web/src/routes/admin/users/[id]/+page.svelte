<script>
  // User detail lives in a drawer on the Users page now.
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import { goto } from '$app/navigation'
  onMount(() => goto(`/admin/users?id=${page.params.id}`, { replaceState: true }))
</script>
