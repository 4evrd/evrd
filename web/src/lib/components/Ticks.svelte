<script>
  let { level = 0, total = 10, tone = '', label = '' } = $props()
</script>

<span class="ticks {tone}" role="img" aria-label={label || `Activity ${level} of ${total}`}>{#each Array(total) as _, i}<i class:on={i < level}></i>{/each}</span>
