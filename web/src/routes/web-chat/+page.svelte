<script>
  // Another site's web chat signs in here (web_chat_origins): the daemon's
  // GET /auth/web-chat sends a browser that isn't signed in to this page,
  // the layout signs it in (password or SSO), and this hands it straight
  // back to the daemon, which makes the web chat's token and returns to it.
  import { onMount } from 'svelte'
  import { t } from '$lib/i18n.svelte.js'

  onMount(() => location.replace('/auth/web-chat' + location.search))
</script>

<p class="desc" aria-busy="true"><span class="spinner" aria-hidden="true"></span>{t('shell.redirecting')}</p>
