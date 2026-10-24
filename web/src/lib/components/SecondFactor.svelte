<script>
  // Account → Security: a second factor for IRC sign-ins (#91). An
  // authenticator app (TOTP) with one-time recovery codes, and the user's
  // "require it for IRC" toggle, which the admin's irc_mfa can lock on or
  // hide.
  import { onMount } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Switch from './Switch.svelte'
  import { api } from '$lib/api.js'
  import { toast } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let st = $state(null) // GET /api/me/mfa
  let enrol = $state(null) // {secret, url, qr} while setting up
  let codes = $state(null) // recovery codes, shown once
  let code = $state('') // a code from the app (to confirm, renew codes, remove)
  let err = $state('')
  let busy = $state(false)

  onMount(async () => { try { st = await api.mfa() } catch { st = null } })

  async function run(fn) {
    err = ''
    busy = true
    try { return await fn() } catch (x) { err = x.message } finally { busy = false }
  }
  const start = () => run(async () => { enrol = await api.startTOTP(); code = '' })
  const confirm = (e) => { e.preventDefault(); return run(async () => {
    codes = (await api.confirmTOTP(code)).recoveryCodes
    enrol = null; code = ''
    st = await api.mfa()
    toast(t('mfa.appOn'))
  }) }
  const renew = () => run(async () => { codes = (await api.newRecoveryCodes(code)).recoveryCodes; code = ''; st = await api.mfa() })
  const remove = () => run(async () => { st = await api.removeTOTP(code); code = ''; codes = null; toast(t('mfa.appOff')) })
  const toggle = (on) => run(async () => { st = await api.updateMFA({ ircRequireMFA: on }) })
  function copyCodes() { navigator.clipboard?.writeText(codes.join('\n')); toast(t('mfa.codesCopied')) }
  const codesFile = $derived(codes ? 'data:text/plain;charset=utf-8,' + encodeURIComponent(codes.join('\n') + '\n') : '')
  const qr = $derived(enrol ? 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(enrol.qr) : '')
</script>

{#if st && st.choice !== 'hidden'}
  <div class="panel-grid" data-testid="second-factor">
    <div class="od-stack">
      <h2>{t('mfa.title')}</h2>
      <p class="desc">{t('mfa.help')}</p>
      {#if st.choice === 'locked'}<p class="desc" data-testid="mfa-locked">{t('mfa.requiredByAdmin')}</p>{/if}
      {#if !st.clientPasswords}<p class="desc">{t('mfa.clientPasswordsExempt')}</p>{/if}
    </div>
    <div class="od-stack" style="--od-gap:var(--s4)">
      <div class="row-line">
        <span class="od-field"><span id="mfa-l">{t('mfa.require')}</span><span>{st.choice === 'locked' ? t('mfa.requireLocked') : st.totp ? t('mfa.requireHelp') : t('mfa.requireNeedsApp')}</span></span>
        <Switch labelledby="mfa-l" checked={st.choice === 'locked' || st.ircRequireMFA} disabled={busy || st.choice === 'locked' || !st.totp && !st.ircRequireMFA} onchange={toggle} id="mfa-require" />
      </div>

      {#if codes}
        <div class="callout" role="note" data-testid="recovery-codes">
          <Icon name="key" />
          <div class="od-fill od-stack" style="--od-gap:8px">
            <b>{t('mfa.codesTitle')}</b><span>{t('mfa.codesHelp')}</span>
            <ul class="codes mono">{#each codes as c (c)}<li>{c}</li>{/each}</ul>
            <div class="od-cluster" style="gap:8px">
              <button class="btn btn-sm" type="button" onclick={copyCodes}><Icon name="copy" />{t('mfa.copy')}</button>
              <a class="btn btn-sm" href={codesFile} download="evrd-recovery-codes.txt"><Icon name="download" />{t('mfa.download')}</a>
              <button class="btn btn-primary btn-sm" type="button" onclick={() => (codes = null)}>{t('mfa.saved')}</button>
            </div>
          </div>
        </div>
      {/if}

      {#if st.totp}
        <p data-testid="mfa-status"><Icon name="check" /> {t('mfa.appIsOn', { count: st.recoveryCodes })}</p>
        <Field id="mfa-code" label={t('mfa.codeLabel')} help={t('mfa.codeHelp')} {err}>
          <input class="input mono" id="mfa-code" inputmode="numeric" autocomplete="one-time-code" spellcheck="false" bind:value={code} />
        </Field>
        <div class="od-cluster" style="gap:8px">
          <button class="btn" type="button" disabled={busy || !code.trim()} onclick={renew}>{t('mfa.newCodes')}</button>
          <button class="btn btn-danger-ghost" type="button" disabled={busy || !code.trim()} onclick={remove}><Icon name="trash" />{t('mfa.remove')}</button>
        </div>
      {:else if enrol}
        <form class="od-stack" style="--od-gap:var(--s3)" onsubmit={confirm} data-testid="mfa-enrol">
          <p>{t('mfa.scan')}</p>
          <img class="qr" src={qr} alt={t('mfa.qrAlt')} width="192" height="192" />
          <p class="desc">{t('mfa.orType')} <span class="code" data-testid="mfa-secret">{enrol.secret}</span></p>
          <Field id="mfa-first" label={t('mfa.firstCode')} {err}>
            <input class="input mono" id="mfa-first" inputmode="numeric" autocomplete="one-time-code" spellcheck="false" bind:value={code} />
          </Field>
          <div class="od-cluster" style="gap:8px">
            <button class="btn btn-primary" type="submit" disabled={busy || !code.trim()}>{t('mfa.turnOn')}</button>
            <button class="btn btn-ghost" type="button" onclick={() => { enrol = null; err = '' }}>{t('common.cancel')}</button>
          </div>
        </form>
      {:else}
        <div><button class="btn" type="button" disabled={busy} onclick={start}><Icon name="key" />{t('mfa.setUp')}</button></div>
        {#if err}<p class="err" role="alert">{err}</p>{/if}
      {/if}
    </div>
  </div>
{/if}

<style>
  .row-line { display: flex; align-items: center; justify-content: space-between; gap: var(--s4); }
  .codes { display: grid; grid-template-columns: repeat(2, max-content); gap: 4px 24px; margin: 0; padding: 0; list-style: none; }
  .qr { width: 192px; height: 192px; background: #fff; padding: 8px; border-radius: 8px; }
  .err { color: var(--bad, oklch(60% 0.2 27)); }
</style>
