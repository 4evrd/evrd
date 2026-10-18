<script>
  import Icon from './Icon.svelte'
  import { toasts } from '$lib/ui.svelte.js'
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="toast {t.kind === 'err' || t.kind === 'bad' ? 'bad' : ''}" class:out={t.out}>
      <Icon name={t.kind === 'err' || t.kind === 'bad' ? 'alert' : 'check'} /><span>{t.text}</span>
    </div>
  {/each}
</div>
