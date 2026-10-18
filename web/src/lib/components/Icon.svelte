<script>
  import { ICONS } from '$lib/icons.js'
  let { name, class: cls = '' } = $props()
</script>

<svg class="i {cls}" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html ICONS[name] || ''}</svg>
