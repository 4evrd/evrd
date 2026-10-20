<script>
  import Icon from './Icon.svelte'
  import { toast } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'
  let { text, label = '', size = 40 } = $props()
  let done = $state(false)
  async function copy() {
    try { await navigator.clipboard.writeText(text) } catch { /* clipboard may be blocked; still confirm */ }
    done = true
    toast(t('ui.copiedToClipboard'))
    setTimeout(() => (done = false), 1400)
  }
</script>

<button class="icon-btn" type="button" onclick={copy} aria-label={done ? t('ui.copied') : label || t('ui.copy')} style="width:{size}px;height:{size}px"><Icon name={done ? 'check' : 'copy'} /></button>
