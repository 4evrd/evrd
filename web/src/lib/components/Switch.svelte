<script>
  let { checked = false, onchange, label = '', labelledby = '', busy = false, id = undefined, disabled = false } = $props()
</script>

<button class="switch" type="button" role="switch" {id} aria-checked={checked} aria-label={label || undefined}
  aria-labelledby={labelledby || undefined} aria-busy={busy ? 'true' : undefined} {disabled}
  onclick={() => onchange?.(!checked)}></button>
