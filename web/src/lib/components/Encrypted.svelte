<script>
  // A lock beside a field evrd stores encrypted (under secret_key). Shown
  // only when a key is set — without one these are saved as plaintext.
  import Icon from './Icon.svelte'
  import { session } from '$lib/session.svelte.js'
  import { t } from '$lib/i18n.svelte.js'
</script>

{#if session.config?.secretsEncrypted}
  <!-- a button so the tip shows on keyboard focus too; it does nothing when pressed -->
  <button type="button" class="enc" aria-label={t('networks.encryptedLabel')} data-tip={t('networks.encryptedTip')} data-testid="encrypted"><Icon name="lock" class="i-sm" /></button>
{/if}
