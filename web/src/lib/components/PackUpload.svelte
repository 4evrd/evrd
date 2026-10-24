<script>
  // Upload a translation file: read it here in any format langformats.js
  // knows, show what's in it against English (errors block saving), ask for
  // the language's code and name when the file doesn't say, then
  // save(pack) with the pack the server takes.
  import { tick } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Select from './Select.svelte'
  import { FORMATS, FormatError, parseFile } from '$lib/langformats.js'
  import { report } from '$lib/langpack.js'
  import { en, english, t, i18n, languages } from '$lib/i18n.svelte.js'

  let { id, label, save, code: preset = '', small = true, primary = false } = $props()

  const DOCS = 'https://github.com/4evrd/evrd/blob/main/docs/translations.md'
  const LIST = 6 // keys shown before "…"

  let input = $state()
  let dlg = $state()
  let file = $state(null) // { filename, format, code, name, fuzzy, unmatched, messages } or { filename, error }
  // change: the file goes into a language on offer (keeping its name);
  // new: it adds one, with the code and name given here
  let mode = $state('change')
  let target = $state('')
  let code = $state('')
  let name = $state('')
  const list = $derived(languages())
  const existing = $derived(list.find((l) => l.code === code.trim()))
  let saving = $state(false)
  let err = $state('')
  // all: the English a pack is checked against, evrd's IRC texts included
  // once they've loaded (picked)
  let all = $state(en)

  const pack = $derived.by(() => {
    if (!file || file.error) return null
    if (mode === 'change') return { code: target, name: list.find((l) => l.code === target)?.name || target, messages: file.messages }
    return { code: code.trim(), name: name.trim(), messages: file.messages }
  })
  const rep = $derived(pack ? report(pack, all) : null)
  const blocked = $derived(!rep || rep.errors.length > 0 || (mode === 'new' && !!existing))
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
      all = await english()
      const got = parseFile(f.name, await f.text(), all)
      file = { ...got, filename: f.name }
      // a language on offer: change it; one evrd doesn't have: add it;
      // the file doesn't say: change the one asked for (or the current)
      const found = got.code || preset
      if (found && !list.some((l) => l.code === found)) {
        mode = 'new'
        target = preset || i18n.locale
      } else {
        mode = 'change'
        target = found || i18n.locale
      }
      code = got.code && mode === 'new' ? got.code : ''
      name = mode === 'new' ? got.name : ''
    } catch (x) {
      file = { filename: f.name, error: x instanceof FormatError ? t(`packs.err.${x.kind}`, x.vars) : t('packs.err.read') }
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
    if (!pack || blocked) return
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
      <p class="mono pu-name">{file.filename}{#if file.format}{' · '}{formatLabel(file.format)}{/if}</p>
      {#if file.error}
        <div class="callout bad" role="alert"><Icon name="alert" /><span>{file.error}</span></div>
      {:else}
        <div class="pu-modes" role="radiogroup" aria-label={t('packs.modeLabel')}>
          <label class="pu-mode" class:on={mode === 'change'}>
            <input type="radio" name="{id}-mode" value="change" bind:group={mode} data-testid="{id}-mode-change" />
            <span class="od-field"><span>{t('packs.modeChange')}</span><span>{t('packs.modeChangeHelp')}</span></span>
          </label>
          <label class="pu-mode" class:on={mode === 'new'}>
            <input type="radio" name="{id}-mode" value="new" bind:group={mode} data-testid="{id}-mode-new" />
            <span class="od-field"><span>{t('packs.modeNew')}</span><span>{t('packs.modeNewHelp')}</span></span>
          </label>
        </div>
        {#if mode === 'change'}
          <Field id="{id}-target" label={t('packs.language')}>
            <Select id="{id}-target" bind:value={target} options={list.map((l) => ({ value: l.code, label: l.name, sub: l.code }))} testid="{id}-target" />
          </Field>
        {:else}
          <div class="form-grid pu-grid">
            <Field id="{id}-code" label={t('packs.code')} help={t('packs.codeHelp')}><input class="input mono" id="{id}-code" spellcheck="false" autocomplete="off" bind:value={code} /></Field>
            <Field id="{id}-name" label={t('packs.name')} help={t('packs.nameHelp')}><input class="input" id="{id}-name" autocomplete="off" bind:value={name} /></Field>
          </div>
          {#if existing}<div class="callout warn" role="status" data-testid="{id}-exists"><Icon name="alert" /><span>{t('packs.exists', { name: existing.name })}</span></div>{/if}
        {/if}
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
      {#if !file.error}<button class="btn btn-primary" type="button" onclick={submit} disabled={saving || blocked}>{#if saving}<span class="spinner" aria-hidden="true"></span>{t('common.saving')}{:else}<Icon name="check" />{t('packs.save')}{/if}</button>{/if}
    </div>
  {/if}
</dialog>

<style>
  .pu-file { display: none; }
  dialog.pu { width: min(560px, calc(100vw - 32px)); }
  .pu-name { color: var(--muted); font-size: var(--fs-xs); overflow-wrap: anywhere; }
  .pu-grid { grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); }
  .pu-modes { display: grid; grid-template-columns: 1fr 1fr; gap: var(--s2); }
  .pu-mode { display: flex; gap: var(--s2); align-items: flex-start; padding: var(--s3); border: 1px solid var(--line-soft); border-radius: var(--r-row); cursor: pointer; font-size: var(--fs-sm); }
  .pu-mode.on { border-color: var(--primary); background: var(--row); }
  .pu-mode input { margin-top: 3px; accent-color: var(--primary); }
  .pu-mode .od-field span:last-child { font-size: var(--fs-xs); color: var(--muted); }
  .pu-report { display: grid; gap: var(--s2); margin: 0; padding: 0; list-style: none; font-size: var(--fs-sm); color: var(--muted); max-height: 40vh; overflow-y: auto; }
  .pu-report li { display: flex; gap: var(--s2); align-items: flex-start; overflow-wrap: anywhere; }
  .pu-report li :global(.i) { flex: none; margin-top: 3px; }
  .pu-report li.bad { color: var(--fg); }
  .pu-report li.bad :global(.i) { color: var(--bad); }
  .pu-report li.warn :global(.i) { color: var(--warn); }
  @media (max-width: 559px) { .pu-grid, .pu-modes { grid-template-columns: minmax(0, 1fr); } }
</style>
