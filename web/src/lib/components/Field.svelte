<script>
  // A labelled form field: shows help text, and an inline error once err is set.
  let { id, label, required = false, help = '', err = '', children } = $props()
</script>

<div class="field" class:invalid={!!err}>
  <label for={id}>{label}{#if required}&nbsp;<span class="req" aria-hidden="true">*</span>{/if}</label>
  {@render children()}
  {#if help}<span class="help" id="{id}-help">{help}</span>{/if}
  <span class="err" id="{id}-err" role="alert">{err}</span>
</div>
