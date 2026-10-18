<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import UserOverview from '$lib/components/UserOverview.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { greeting } from '$lib/format.js'

  let ov = $state(null)
  let err = $state('')
  let timer
  async function load() {
    try { ov = await api.overview(); err = '' } catch (e) { err = e.message }
  }
  onMount(() => { load(); timer = setInterval(load, 15000) })
  onDestroy(() => clearInterval(timer))

  const me = $derived(session.me)
  const nets = $derived(ov?.networks || [])
  const connected = $derived(nets.filter((n) => n.status === 'connected').length)
  const clients = $derived(ov?.clients || [])
</script>

<svelte:head><title>Overview · evrd</title></svelte:head>

<div class="page-head enter">
  <div class="titles">
    <h1>Overview</h1>
    <p>{greeting()}, {me.username}. {#if ov}{connected} of {nets.length} network{nets.length === 1 ? ' is' : 's are'} connected and {clients.length} client{clients.length === 1 ? ' is' : 's are'} attached.{/if}</p>
  </div>
  <div class="od-cluster">
    <a class="btn" href="/clients"><Icon name="plug" />Connect a client</a>
    <a class="btn btn-primary" href="/networks/new"><Icon name="plus" />Add network</a>
  </div>
</div>

{#if err && !ov}
  <div class="callout bad" role="alert"><Icon name="alert" /><span>Couldn’t load your overview: {err}</span></div>
{/if}

<UserOverview {ov} usage={me.usage} limits={me.limits} />
