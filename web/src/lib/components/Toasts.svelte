<script>
  import Icon from './Icon.svelte'
  import { toasts } from '$lib/ui.svelte.js'
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="toast" class:err={t.kind === 'err'} class:out={t.out}>
      <Icon name={t.kind === 'err' ? 'alert' : 'check'} /><span>{t.text}</span>
    </div>
  {/each}
</div>
