<script>
  // A labelled field: help text, and an inline error once err is set.
  // help is plain text (it's often translated, and translations can be
  // uploaded); for help with markup, pass a helpBody snippet instead.
  // encrypted marks a value evrd stores encrypted (see Encrypted).
  import Icon from './Icon.svelte'
  import Encrypted from './Encrypted.svelte'
  let { id, label, required = false, help = '', helpBody = null, err = '', span = false, encrypted = false, children } = $props()
</script>

<div class="field" class:invalid={!!err} class:span-2={span}>
  <div class="label-row">
    <label for={id}>{label}{#if required}<span class="req" aria-hidden="true">*</span>{/if}</label>
    {#if encrypted}<Encrypted />{/if}
  </div>
  {@render children()}
  {#if helpBody}<span class="help" id="{id}-help">{@render helpBody()}</span>{:else if help}<span class="help" id="{id}-help">{help}</span>{/if}
  <span class="err" id="{id}-err" role="alert">{#if err}<Icon name="alert" class="i-sm" /><span>{err}</span>{/if}</span>
</div>
