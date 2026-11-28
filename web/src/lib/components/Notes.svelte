<script>
  // The account's notes (ZNC's notes module): the same ones /msg *notes
  // lists, adds and deletes, and whether IRC clients are shown them as
  // they connect (settings.showNotesOnLogin).
  import { onMount } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Switch from './Switch.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { toast } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let notes = $state(null)
  let key = $state('')
  let text = $state('')
  let errs = $state({})
  let busy = $state(false)
  let keyEl = $state()

  async function load() { try { notes = await api.notes() } catch (e) { notes = []; toast(e.message, 'bad') } }
  onMount(load)

  async function save(e) {
    e.preventDefault()
    const k = key.trim()
    errs = { key: !k || /\s/.test(k) || /^[+-]/.test(k) ? t('notes.keyInvalid') : '', text: text.trim() ? '' : t('notes.textMissing') }
    if (errs.key || errs.text) return
    busy = true
    try {
      notes = await api.putNote(k, text.trim())
      toast(t('notes.saved', { key: k }))
      key = ''
      text = ''
    } catch (err) { toast(err.message, 'bad') } finally { busy = false }
  }
  function edit(n) {
    key = n.key
    text = n.text
    keyEl?.focus()
  }
  async function remove(n) {
    try {
      await api.deleteNote(n.key)
      notes = notes.filter((x) => x.key !== n.key)
      toast(t('notes.deleted', { key: n.key }))
    } catch (e) { toast(e.message, 'bad') }
  }
  async function showOnLogin(v) {
    try { session.me = await api.updateMe({ settings: { showNotesOnLogin: v } }) } catch (e) { toast(e.message, 'bad') }
  }
</script>

<div class="notes" data-testid="notes">
  <div class="panel-grid">
    <div class="od-stack"><h2>{t('notes.heading')}</h2><p class="desc">{t('notes.intro')}</p></div>
    <div class="od-stack" style="--od-gap:16px">
      <div class="check-row"><span class="od-field"><span id="n-login-l">{t('notes.showOnLogin')}</span><span>{t('notes.showOnLoginHelp')}</span></span><Switch checked={session.me.settings.showNotesOnLogin} labelledby="n-login-l" onchange={showOnLogin} /></div>

      <div class="rows">
        {#if !notes}<div class="skel"></div>
        {:else if !notes.length}<p class="desc" data-testid="notes-none">{t('notes.none')}</p>
        {:else}
          {#each notes as n (n.key)}
            <div class="row note-row" data-testid="note">
              <span class="mono key">{n.key}</span>
              <span class="text">{n.text}</span>
              <span class="od-cluster" style="gap:4px">
                <button class="icon-btn" type="button" aria-label={t('notes.edit', { key: n.key })} onclick={() => edit(n)}><Icon name="pencil" /></button>
                <button class="icon-btn" type="button" aria-label={t('notes.delete', { key: n.key })} onclick={() => remove(n)}><Icon name="trash" /></button>
              </span>
            </div>
          {/each}
        {/if}
      </div>

      <form class="form-grid" novalidate onsubmit={save} aria-label={t('notes.formLabel')}>
        <Field id="n-key" label={t('notes.key')} help={t('notes.keyHelp')} err={errs.key}><input class="input mono" id="n-key" maxlength="64" spellcheck="false" autocomplete="off" bind:value={key} bind:this={keyEl} aria-invalid={!!errs.key} /></Field>
        <Field id="n-text" label={t('notes.text')} err={errs.text} span><input class="input" id="n-text" maxlength="400" autocomplete="off" bind:value={text} aria-invalid={!!errs.text} /></Field>
        <div class="span-2"><button class="btn btn-primary" type="submit" disabled={busy}>{#if busy}<span class="spinner" aria-hidden="true"></span>{:else}<Icon name="check" />{/if}{t('notes.save')}</button></div>
      </form>
    </div>
  </div>
</div>

<style>
  .panel-grid { display: grid; grid-template-columns: minmax(0, 280px) minmax(0, 1fr); gap: var(--s7); }
  .panel-grid :global(.od-stack) { --od-gap: 6px; }
  .note-row { --cols: minmax(80px, auto) minmax(0, 1fr) auto; }
  .note-row .key { color: var(--muted); }
  .note-row .text { overflow-wrap: anywhere; }
  @media (max-width: 959px) { .panel-grid { grid-template-columns: minmax(0, 1fr); gap: var(--s4); } }
</style>
