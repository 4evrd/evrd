<script>
  const STATUS = {
    connected: { cls: 'ok live', label: 'Connected' },
    reconnecting: { cls: 'warn', label: 'Reconnecting' },
    connecting: { cls: 'warn', label: 'Connecting' },
    disconnected: { cls: '', label: 'Disconnected' },
    error: { cls: 'bad', label: 'Error' },
  }
  let { status } = $props()
  const s = $derived(STATUS[status] || STATUS.disconnected)
</script>

<span class="chip {s.cls}" data-testid="status-chip"><span class="dot" aria-hidden="true"></span>{s.label}</span>
