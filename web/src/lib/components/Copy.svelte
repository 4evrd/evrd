<script>
  import Icon from './Icon.svelte'
  import { toast } from '$lib/ui.svelte.js'
  let { text, label = 'Copy', size = 40 } = $props()
  let done = $state(false)
  async function copy() {
    try { await navigator.clipboard.writeText(text) } catch { /* clipboard may be blocked; still confirm */ }
    done = true
    toast('Copied to clipboard')
    setTimeout(() => (done = false), 1400)
  }
</script>

<button class="icon-btn" type="button" onclick={copy} aria-label={done ? 'Copied' : label} style="width:{size}px;height:{size}px"><Icon name={done ? 'check' : 'copy'} /></button>
