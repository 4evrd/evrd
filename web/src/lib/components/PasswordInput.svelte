<script>
  // A password input with an eye to show what's typed, and hide it again.
  // Takes the input's own attributes (id, class, autocomplete, oninput…);
  // value can be bound.
  import Icon from './Icon.svelte'
  import { t } from '$lib/i18n.svelte.js'
  let { value = $bindable(''), class: cls = 'input', ...rest } = $props()
  let shown = $state(false)
</script>

<div class="pw">
  <input class={cls} {...rest} type={shown ? 'text' : 'password'} spellcheck="false" autocapitalize="none" bind:value />
  <button class="pw-eye" type="button" aria-label={t('common.showPassword')} aria-pressed={shown} aria-controls={rest.id} title={shown ? t('common.hidePassword') : t('common.showPassword')} onclick={() => (shown = !shown)}><Icon name={shown ? 'eyeOff' : 'eye'} /></button>
</div>
