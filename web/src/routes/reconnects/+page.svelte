<script>
  // Which networks reconnected in the last day, and why.
  import { onMount, onDestroy } from 'svelte'
  import Reconnects from '$lib/components/Reconnects.svelte'
  import { api } from '$lib/api.js'
  import { toast } from '$lib/ui.svelte.js'

  let data = $state(null)
  let timer
  async function load() {
    try { data = await api.reconnects() } catch (e) { if (!data) toast(e.message, 'bad') }
  }
  onMount(() => { load(); timer = setInterval(load, 15000) })
  onDestroy(() => clearInterval(timer))
</script>

<svelte:head><title>Reconnects · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Reconnects</h1>
    <p>Your networks’ last 24 hours: links that dropped, connection attempts that failed, and why.</p>
  </div>
</div>

<section class="card enter" aria-label="Reconnects by network">
  <Reconnects {data} logHref={(n) => `/networks/${n.id}#log`} />
</section>
