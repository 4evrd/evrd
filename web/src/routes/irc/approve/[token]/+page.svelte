<script>
  // IRC sign-in by link (#91): an IRC client without a password it could
  // use was sent here. Its owner, signed in (the layout asks first, SSO and
  // its MFA included), allows it — remembering the device if they like —
  // or turns it away.
  import { onMount } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast } from '$lib/ui.svelte.js'
  import { ago } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  const token = $derived(page.params.token)
  let p = $state(null) // the waiting client
  let gone = $state('') // why it can't be decided (expired, used…)
  let done = $state('') // 'allowed' | 'denied'
  let password = $state('') // a new client password, shown once
  let remember = $state('password')
  let busy = $state(false)

  onMount(async () => {
    try {
      p = await api.ircSignIn(token)
      if (p.state !== 'pending') gone = t('signin.used')
      if (!p.canRememberSecret && remember === 'secret') remember = 'password'
    } catch (x) { gone = x.message }
  })

  async function allow() {
    busy = true
    try {
      const r = await api.allowIRCSignIn(token, remember === 'none' ? '' : remember)
      password = r.clientPassword || ''
      done = 'allowed'
    } catch (x) { toast(x.message, 'bad') } finally { busy = false }
  }
  async function deny() {
    busy = true
    try { await api.denyIRCSignIn(token); done = 'denied' } catch (x) { toast(x.message, 'bad') } finally { busy = false }
  }
  function copy() { navigator.clipboard?.writeText(password); toast(t('signin.copied')) }
  const login = $derived(p ? `${session.me.username}${p.network ? '/' + p.network : ''}:` : '')
</script>

<svelte:head><title>{t('signin.title')} · evrd</title></svelte:head>

<div class="approve">
  <h1>{t('signin.title')}</h1>
  {#if gone}
    <div class="callout warn" role="alert" data-testid="signin-gone"><Icon name="alert" /><span>{gone}</span></div>
  {:else if !p}
    <p aria-busy="true">{t('common.loading')}</p>
  {:else if done === 'denied'}
    <div class="callout" role="status" data-testid="signin-denied"><Icon name="check" /><span>{t('signin.deniedDone')}</span></div>
  {:else if done === 'allowed'}
    <div class="callout ok" role="status" data-testid="signin-allowed"><Icon name="check" /><span>{t('signin.allowedDone')}</span></div>
    {#if password}
      <div class="card od-stack" style="--od-gap:var(--s3)" data-testid="signin-password">
        <h2>{t('signin.passwordTitle')}</h2>
        <p>{t('signin.passwordHelp', { days: p.rememberDays })}</p>
        <p class="code big mono">{login}{password}</p>
        <div><button class="btn" type="button" onclick={copy}><Icon name="copy" />{t('signin.copy')}</button></div>
      </div>
    {/if}
  {:else}
    <div class="card od-stack" style="--od-gap:var(--s4)">
      <p>{p.secondFactor ? t('signin.introSecondFactor') : t('signin.intro')}</p>
      <dl class="facts">
        {#if p.nick}<dt>{t('signin.nick')}</dt><dd class="mono" data-testid="signin-nick">{p.nick}</dd>{/if}
        {#if p.network}<dt>{t('signin.network')}</dt><dd class="mono">{p.network}</dd>{/if}
        {#if p.username}<dt>{t('signin.account')}</dt><dd class="mono">{p.username}</dd>{/if}
        <dt>{t('signin.from')}</dt><dd class="mono" data-testid="signin-ip">{p.ip}</dd>
        <dt>{t('signin.when')}</dt><dd>{ago(p.createdAt)}</dd>
      </dl>
      <div class="callout warn" role="note"><Icon name="alert" /><span><b>{t('signin.warnTitle')}</b> {t('signin.warn')}</span></div>
      {#if !p.sameAddress}<p class="desc" data-testid="signin-other-address">{t('signin.otherAddress', { ip: p.ip })}</p>{/if}
      {#if !p.yours}
        <div class="callout warn" role="alert" data-testid="signin-not-yours"><Icon name="alert" /><span>{t('signin.notYoursHelp', { account: p.username, me: session.me.username })}</span></div>
        <div><button class="btn btn-danger-ghost" type="button" disabled={busy} onclick={deny}>{t('signin.deny')}</button></div>
      {:else}
        {#if !p.secondFactor}
          <fieldset class="od-stack" style="--od-gap:8px">
            <legend>{t('signin.rememberTitle', { days: p.rememberDays })}</legend>
            <label><input type="radio" bind:group={remember} value="password" /> {t('signin.rememberPassword')}</label>
            {#if p.canRememberSecret}<label><input type="radio" bind:group={remember} value="secret" /> {t('signin.rememberSecret')}</label>{/if}
            <label><input type="radio" bind:group={remember} value="none" /> {t('signin.rememberNone')}</label>
          </fieldset>
        {/if}
        <div class="od-cluster" style="gap:8px">
          <button class="btn btn-primary" type="button" disabled={busy} onclick={allow}><Icon name="check" />{t('signin.allow')}</button>
          <button class="btn btn-danger-ghost" type="button" disabled={busy} onclick={deny}>{t('signin.deny')}</button>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .approve { max-width: 560px; display: grid; gap: var(--s5); }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: 6px var(--s4); margin: 0; }
  .facts dt { color: var(--muted, inherit); }
  .facts dd { margin: 0; word-break: break-all; }
  .big { font-size: var(--fs-md); word-break: break-all; }
  fieldset { border: 0; padding: 0; margin: 0; }
  legend { font-weight: 500; margin-bottom: 4px; }
</style>
