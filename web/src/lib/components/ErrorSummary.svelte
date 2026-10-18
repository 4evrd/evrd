<script>
  import Icon from './Icon.svelte'
  // errs: [[fieldId, message], …]; shown only when more than one field is wrong.
  let { errs = [] } = $props()
</script>

<div class="callout err err-summary" class:show={errs.length > 1} role="alert">
  {#if errs.length > 1}
    <Icon name="alert" />
    <div class="body"><b>Fix these first</b>
      <ul>{#each errs as [id, m]}<li><a href="#{id}">{m}</a></li>{/each}</ul>
    </div>
  {/if}
</div>
