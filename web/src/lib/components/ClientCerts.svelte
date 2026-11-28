<script>
  // Account → Security: TLS client certificates that sign IRC clients in
  // without a password (certauth: SASL EXTERNAL, or no password at all).
  // Each is known by its SHA-256 fingerprint; the certificates the user's
  // attached IRC clients sent are offered to add with a click.
  import { onMount } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import { api } from '$lib/api.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { date, ago } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  const GUIDE = 'https://github.com/4evrd/evrd/blob/main/docs/irc-clients.md#signing-in-with-a-client-certificate'

  let st = $state(null) // GET /api/me/client-certs: { certs, clients }
  let fingerprint = $state('')
  let label = $state('')
  let err = $state('')
  let busy = $state(false)

  async function load() { try { st = await api.clientCerts() } catch { st = null } }
  onMount(load)

  const pending = $derived((st?.clients || []).filter((c) => !c.added))
  // AB:CD:… is how most tools show a fingerprint; any spelling is taken
  const show = (fp) => fp.match(/.{2}/g).join(':').toUpperCase()

  async function add(fp, name) {
    err = ''
    busy = true
    try {
      await api.addClientCert(fp, name)
      fingerprint = ''
      label = ''
      await load()
      toast(t('certs.added'))
    } catch (x) { err = x.message.charAt(0).toUpperCase() + x.message.slice(1) } finally { busy = false }
  }
  function submit(e) { e.preventDefault(); add(fingerprint.trim(), label.trim()) }
  async function remove(c) {
    const ok = await confirmDialog({ title: t('certs.removeTitle'), body: t('certs.removeBody'), action: t('certs.remove') })
    if (!ok) return
    try { await api.deleteClientCert(c.id); await load(); toast(t('certs.removed')) } catch (x) { toast(x.message, 'bad') }
  }
</script>

{#if st}
  <hr class="divider" />
  <div class="panel-grid" data-testid="client-certs">
    <div class="od-stack">
      <h2>{t('certs.title')}</h2>
      <p class="desc">{t('certs.help')}</p>
      <p class="desc">{t('certs.mfa')}</p>
      <p class="desc"><a href={GUIDE} target="_blank" rel="noreferrer">{t('certs.guide')}</a></p>
    </div>
    <div class="od-stack" style="--od-gap:var(--s4)">
      {#each pending as c (c.fingerprint)}
        <div class="callout" role="note" data-testid="cert-offer">
          <Icon name="shield" />
          <div class="od-fill od-stack" style="--od-gap:8px">
            <b>{t('certs.clientUses', { client: c.software || t('certs.ircClient'), ip: c.ip || '?' })}</b>
            <span class="mono fp">{show(c.fingerprint)}</span>
            <div><button class="btn btn-primary btn-sm" type="button" disabled={busy} onclick={() => add(c.fingerprint, c.device || c.software || '')}><Icon name="plus" />{t('certs.addThis')}</button></div>
          </div>
        </div>
      {/each}

      <div class="rows">
        {#each st.certs as c (c.id)}
          <div class="row cert-row" data-testid="cert-row">
            <span class="cic"><Icon name="shield" /></span>
            <span class="od-field">
              <span class="name od-truncate">{c.label || t('certs.unnamed')}</span>
              <span class="sub mono fp">{show(c.fingerprint)}</span>
              <span class="sub">{t('certs.addedUsed', { added: date(c.createdAt), used: ago(c.lastUsedAt) })}</span>
            </span>
            <button class="btn btn-danger-ghost btn-sm" type="button" aria-label={t('certs.removeName', { name: c.label || show(c.fingerprint) })} onclick={() => remove(c)}>{t('certs.remove')}</button>
          </div>
        {:else}
          <p class="desc" data-testid="certs-none">{t('certs.none')}</p>
        {/each}
      </div>

      <form class="od-stack" style="--od-gap:var(--s3)" onsubmit={submit} novalidate>
        <Field id="cert-fp" label={t('certs.fingerprint')} help={t('certs.fingerprintHelp')} {err}>
          <input class="input mono" id="cert-fp" spellcheck="false" autocomplete="off" placeholder="AB:CD:…" bind:value={fingerprint} aria-invalid={!!err} />
        </Field>
        <Field id="cert-label" label={t('certs.label')} help={t('certs.labelHelp')}>
          <input class="input" id="cert-label" maxlength="64" bind:value={label} />
        </Field>
        <div><button class="btn" type="submit" disabled={busy || !fingerprint.trim()}><Icon name="plus" />{t('certs.addButton')}</button></div>
      </form>
    </div>
  </div>
{/if}

<style>
  .cert-row { --cols: 36px minmax(0, 1fr) auto; }
  .cic { display: grid; place-items: center; width: 36px; height: 36px; border-radius: 10px; background: var(--bg-deep); color: var(--muted); }
  .fp { overflow-wrap: anywhere; font-size: 12px; }
</style>
