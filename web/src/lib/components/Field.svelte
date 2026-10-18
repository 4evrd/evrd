<script>
  // A labelled field: help text, and an inline error once err is set.
  import Icon from './Icon.svelte'
  let { id, label, required = false, help = '', err = '', span = false, children } = $props()
</script>

<div class="field" class:invalid={!!err} class:span-2={span}>
  <label for={id}>{label}{#if required}<span class="req" aria-hidden="true">*</span>{/if}</label>
  {@render children()}
  {#if help}<span class="help" id="{id}-help">{@html help}</span>{/if}
  <span class="err" id="{id}-err" role="alert">{#if err}<Icon name="alert" class="i-sm" /><span>{err}</span>{/if}</span>
</div>
