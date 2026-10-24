<script>
  // A download button with a menu of translation file formats
  // (langformats.js). load() is the pack to write ({ code, name, messages });
  // without it, the template: every English key (the app's and evrd's IRC
  // texts), nothing translated.
  import { tick } from 'svelte'
  import Icon from './Icon.svelte'
  import { FORMATS, exportFile } from '$lib/langformats.js'
  import { english, t } from '$lib/i18n.svelte.js'
  import { toast } from '$lib/ui.svelte.js'

  let { id, label, load = null, small = true, ghost = false, aria = '' } = $props()

  let open = $state(false)
  let root = $state()

  async function show() {
    open = !open
    if (open) { await tick(); root?.querySelector('[role="menuitem"]')?.focus() }
  }
  function hide(focus = true) {
    open = false
    if (focus) root?.querySelector('button')?.focus()
  }
  function key(e) {
    const items = [...(root?.querySelectorAll('[role="menuitem"]') || [])]
    const i = items.indexOf(document.activeElement)
    if (e.key === 'Escape') { e.preventDefault(); hide() }
    else if (e.key === 'ArrowDown') { e.preventDefault(); items[(i + 1) % items.length]?.focus() }
    else if (e.key === 'ArrowUp') { e.preventDefault(); items[(i - 1 + items.length) % items.length]?.focus() }
  }
  async function pick(format) {
    hide()
    try {
      const [pack, en] = await Promise.all([load ? load() : null, english()])
      const file = exportFile(format, pack ? { ...pack, en } : { en, template: true })
      const url = URL.createObjectURL(new Blob([file.text], { type: `${file.mime};charset=utf-8` }))
      const a = Object.assign(document.createElement('a'), { href: url, download: file.filename })
      document.body.append(a)
      a.click()
      a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (e) { toast(e.message, 'bad') }
  }
</script>

<svelte:window onclick={(e) => open && !root?.contains(e.target) && hide(false)} />

<div class="pd" bind:this={root} onkeydown={key} role="presentation">
  <button class="btn" class:btn-sm={small} class:btn-ghost={ghost} type="button" id={id} aria-haspopup="menu" aria-expanded={open} aria-label={aria || undefined} onclick={show}><Icon name="download" />{label}</button>
  {#if open}
    <div class="dd-list pd-list" role="menu" aria-labelledby={id}>
      {#each FORMATS.filter((f) => !(load && f.templateOnly)) as f}
        <button class="dd-opt" type="button" role="menuitem" onclick={() => pick(f.id)}>{f.label}<span class="sub">.{f.ext}</span></button>
      {/each}
      <span class="sub pd-hint">{t('packs.formatHint')}</span>
    </div>
  {/if}
</div>

<style>
  .pd { position: relative; display: inline-block; }
  .pd-list { right: auto; min-width: 200px; }
  .pd-list .dd-opt { justify-content: space-between; width: 100%; background: none; border: 0; text-align: left; }
  .pd-hint { padding: var(--s1, 4px) var(--s3); color: var(--muted); font-size: var(--fs-xs); }
</style>
