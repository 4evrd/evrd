<script>
  import { cap } from '$lib/format.js'
  // status: a network's connection status (connected | connecting | disconnected | …)
  let { status } = $props()
  const kind = $derived(status === 'connected' ? 'ok' : status === 'disconnected' ? '' : status === 'error' ? 'bad' : 'warn')
</script>

<span class="chip {kind}" class:live={kind === 'ok'} data-testid="status-chip"><span class="dot" aria-hidden="true"></span>{cap(status || 'unknown')}</span>
