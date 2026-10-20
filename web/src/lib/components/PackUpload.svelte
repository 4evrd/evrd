<script>
  // Upload a translation file: read it here in any format langformats.js
  // knows, show what's in it against English (errors block saving), ask for
  // the language's code and name when the file doesn't say, then
  // save(pack) with the pack the server takes.
  import { tick } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import { FORMATS, FormatError, parseFile } from '$lib/langformats.js'
  import { report } from '$lib/langpack.js'
  import { en, t } from '$lib/i18n.svelte.js'

  let { id, label, save, code: preset = '', small = true, primary = false } = $props()

  const DOCS = 'https://github.com/4evrd/evrd/blob/main/docs/translations.md'
  const LIST = 6 // keys shown before "…"

  let input = $state()
  let dlg = $state()
  let file = $state(null) // { name, format, fuzzy, unmatched, messages } or { name, error }
  let code = $state('')
  let name = $state('')
  let saving = $state(false)
  let err = $state('')

  const pack = $derived(file && !file.error ? { code: code.trim(), name: name.trim(), messages: file.messages } : null)
  const rep = $derived(pack ? report(pack, en) : null)
  const keys = (list) => list.slice(0, LIST).join(', ') + (list.length > LIST ? ', …' : '')
  const formatLabel = (f) => (f === 'xliff' ? 'XLIFF' : FORMATS.find((x) => x.id === f)?.label || f)

  function problem(p) {
    return t(`packs.problem.${p.kind}`, { key: p.key, want: p.want })
  }

  async function picked(e) {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ''
    if (!f) return
    err = ''
    try {
      const got = parseFile(f.name, await f.text(), en)
      file = { name: f.name, ...got }
      code = got.code || preset
      name = got.name
    } catch (x) {
      file = { name: f.name, error: x instanceof FormatError ? t(`packs.err.${x.kind}`, x.vars) : t('packs.err.read') }
    }
    dlg.showModal()
    await tick()
    dlg.querySelector('input, button.btn-primary, button')?.focus()
  }

  function close() {
    if (dlg?.open) dlg.close()
    file = null
  }

  async function submit() {
    if (!pack || rep.errors.length) return
    saving = true
    err = ''
    try {
      await save(pack)
      close()
    } catch (x) { err = x.message } finally { saving = false }
  }
</script>

<input class="pu-file" type="file" accept=".json,.csv,.po,.pot,.xlf,.xliff,.yaml,.yml" bind:this={input} onchange={picked} data-testid="{id}-file" tabindex="-1" aria-hidden="true" />
<button class="btn" class:btn-sm={small} class:btn-primary={primary} type="button" {id} onclick={() => input.click()}><Icon name="upload" />{label}</button>

<dialog class="confirm pu" aria-labelledby="{id}-t" bind:this={dlg} onclose={() => (file = null)}>
  {#if file}
    <div class="confirm-body">
      <h2 id="{id}-t">{t('packs.checkTitle')}</h2>
      <p class="mono pu-name">{file.name}{#if file.format} · {formatLabel(file.format)}{/if}</p>
      {#if file.error}
        <div class="callout bad" role="alert"><Icon name="alert" /><span>{file.error}</span></div>
      {:else}
        <div class="form-grid pu-grid">
          <Field id="{id}-code" label={t('packs.code')} help={t('packs.codeHelp')}><input class="input mono" id="{id}-code" spellcheck="false" autocomplete="off" bind:value={code} /></Field>
          <Field id="{id}-name" label={t('packs.name')} help={t('packs.nameHelp')}><input class="input" id="{id}-name" autocomplete="off" bind:value={name} /></Field>
        </div>
        <ul class="pu-report" data-testid="pack-report">
          {#each rep.errors as p}<li class="bad"><Icon name="alert" class="i-sm" /><span>{problem(p)}</span></li>{/each}
          {#if rep.unknown.length}<li class="warn"><Icon name="alert" class="i-sm" /><span>{t('packs.unknown', { count: rep.unknown.length, keys: keys(rep.unknown) })}</span></li>{/if}
          <li><Icon name="check" class="i-sm" /><span>{t('packs.count', { count: rep.count })}</span></li>
          {#if rep.untranslated}<li><Icon name="info" class="i-sm" /><span>{t('packs.untranslated', { count: rep.untranslated, total: rep.total })}</span></li>{/if}
          {#if file.fuzzy}<li><Icon name="info" class="i-sm" /><span>{t('packs.fuzzy', { count: file.fuzzy })}</span></li>{/if}
          {#if file.unmatched}<li class="warn"><Icon name="alert" class="i-sm" /><span>{t('packs.unmatched', { count: file.unmatched })}</span></li>{/if}
        </ul>
        {#if err}<div class="callout bad" role="alert"><Icon name="alert" /><span>{err}</span></div>{/if}
      {/if}
      <p class="help"><a href={DOCS} target="_blank" rel="noreferrer">{t('packs.guide')}</a></p>
    </div>
    <div class="confirm-foot">
      <button class="btn btn-ghost" type="button" onclick={close}>{t('common.cancel')}</button>
      {#if !file.error}<button class="btn btn-primary" type="button" onclick={submit} disabled={saving || rep.errors.length > 0}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('common.saving')}{:else}<Icon name="check" />{t('packs.save')}{/if}</button>{/if}
    </div>
  {/if}
</dialog>

<style>
  .pu-file { display: none; }
  dialog.pu { width: min(560px, calc(100vw - 32px)); }
  .pu-name { color: var(--muted); font-size: var(--fs-xs); overflow-wrap: anywhere; }
  .pu-grid { grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); }
  .pu-report { display: grid; gap: var(--s2); margin: 0; padding: 0; list-style: none; font-size: var(--fs-sm); color: var(--muted); max-height: 40vh; overflow-y: auto; }
  .pu-report li { display: flex; gap: var(--s2); align-items: flex-start; overflow-wrap: anywhere; }
  .pu-report li :global(.i) { flex: none; margin-top: 3px; }
  .pu-report li.bad { color: var(--fg); }
  .pu-report li.bad :global(.i) { color: var(--bad); }
  .pu-report li.warn :global(.i) { color: var(--warn); }
  @media (max-width: 559px) { .pu-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
