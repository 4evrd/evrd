<script>
  import { t } from '$lib/i18n.svelte.js'

  const STATUS = {
    connected: { cls: 'ok live', label: 'networks.status.connected' },
    reconnecting: { cls: 'warn', label: 'networks.status.reconnecting' },
    connecting: { cls: 'warn', label: 'networks.status.connecting' },
    disconnected: { cls: '', label: 'networks.status.disconnected' },
    error: { cls: 'bad', label: 'networks.status.error' },
  }
  let { status } = $props()
  const s = $derived(STATUS[status] || STATUS.disconnected)
</script>

<span class="chip {s.cls}" data-testid="status-chip"><span class="dot" aria-hidden="true"></span>{t(s.label)}</span>
