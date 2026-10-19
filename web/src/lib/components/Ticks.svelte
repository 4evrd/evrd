<script>
  import { t } from '$lib/i18n.svelte.js'
  let { level = 0, total = 10, tone = '', label = '' } = $props()
</script>

<span class="ticks {tone}" role="img" aria-label={label || t('overview.activity', { level, total })}>{#each Array(total) as _, i}<i class:on={i < level}></i>{/each}</span>
