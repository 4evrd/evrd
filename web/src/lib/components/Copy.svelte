<script>
  import Icon from './Icon.svelte'
  let { text, label = 'Copy' } = $props()
  let done = $state(false)
  async function copy() {
    try { await navigator.clipboard.writeText(text) } catch { /* clipboard may be blocked; still confirm */ }
    done = true
    setTimeout(() => (done = false), 1400)
  }
</script>

<button class="copy" class:done type="button" onclick={copy} aria-label={done ? 'Copied' : label}>
  <Icon name={done ? 'check' : 'copy'} />
</button>
