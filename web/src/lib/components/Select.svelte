<script>
  // A dropdown in the app's style, in place of the browser's <select>: a
  // button (role combobox) that opens a list of options. Keyboard as the
  // WAI-ARIA select-only combobox: arrows, Home/End, Enter/Space, Escape,
  // Tab, and typing to jump. Label it with <label for={id}> (Field does).
  //
  // options: [{ value, label, sub?, disabled? }] (any other fields are the
  //          caller's, for the option snippet)
  // option:  a snippet rendering one option's content (default: its label)
  // onchange(value) runs after a pick; value is also bindable.
  import { tick } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import { step, edge, typeahead, indexOf } from '$lib/select.js'
  import { t } from '$lib/i18n.svelte.js'

  let { id, value = $bindable(), options = [], disabled = false, placeholder = '', mono = false, strong = false, onchange = null, option = null, testid = null } = $props()

  let open = $state(false)
  let active = $state(-1) // the highlighted option while open
  let root = $state()
  let list = $state()
  const cur = $derived(indexOf(options, value))
  const listId = $derived(`${id}-list`)
  const optId = (i) => `${id}-opt-${i}`

  async function show(at) {
    if (disabled || !options.length) return
    open = true
    active = at ?? (cur >= 0 ? cur : edge(options, false))
    await tick()
    reveal()
  }
  function hide() { open = false; active = -1 }
  function pick(i) {
    const o = options[i]
    if (!o || o.disabled) return
    hide()
    root?.querySelector('button.dd-trigger')?.focus()
    if (o.value === value) return
    value = o.value
    onchange?.(o.value)
  }
  function reveal() { list?.querySelector(`#${CSS.escape(optId(active))}`)?.scrollIntoView({ block: 'nearest' }) }
  function moveTo(i) { if (i >= 0) { active = i; tick().then(reveal) } }

  let typed = ''
  let typedAt = 0
  function typeKey(ch) {
    const now = Date.now()
    typed = now - typedAt > 700 ? ch : typed + ch
    typedAt = now
    const from = open ? active : cur
    const j = typeahead(options, typed, from)
    if (j < 0) return
    if (open) moveTo(j)
    else pick(j) // closed, like a native select: typing changes the value
  }

  function key(e) {
    if (disabled) return
    const k = e.key
    if (!open) {
      if (k === 'ArrowDown' || k === 'ArrowUp' || k === 'Enter' || k === ' ') { e.preventDefault(); show() }
      else if (k === 'Home' || k === 'End') { e.preventDefault(); show(edge(options, k === 'End')) }
      else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) typeKey(k)
      return
    }
    if (k === 'ArrowDown' || k === 'ArrowUp') { e.preventDefault(); moveTo(step(options, active, k === 'ArrowDown' ? 1 : -1)) }
    else if (k === 'Home' || k === 'End') { e.preventDefault(); moveTo(edge(options, k === 'End')) }
    else if (k === 'Enter' || (k === ' ' && !typed)) { e.preventDefault(); pick(active) }
    else if (k === 'Escape') { e.preventDefault(); hide() }
    else if (k === 'Tab') { pick(active) }
    else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); typeKey(k) }
  }

  function outside(e) { if (open && root && !root.contains(e.target)) hide() }
</script>

<svelte:window onpointerdown={outside} />

<div class="dd" class:open bind:this={root} data-testid={testid}>
  <button type="button" class="select dd-trigger" class:mono class:strong {id} {disabled}
    role="combobox" aria-haspopup="listbox" aria-expanded={open} aria-controls={listId}
    aria-activedescendant={open && active >= 0 ? optId(active) : undefined}
    onclick={() => (open ? hide() : show())} onkeydown={key}>
    <span class="dd-value">
      {#if cur >= 0}
        {#if option}{@render option(options[cur], true)}{:else}<span class="od-truncate">{options[cur].label}</span>{/if}
      {:else}<span class="faint od-truncate">{placeholder || t('ui.choose')}</span>{/if}
    </span>
    <Icon name="chevronDown" class="i-sm dd-chev" />
  </button>
  {#if open}
    <div class="dd-list" id={listId} role="listbox" tabindex="-1" bind:this={list}>
      {#each options as o, i}
        <!-- svelte-ignore a11y_click_events_have_key_events (the combobox button takes the keys: aria-activedescendant) -->
        <div class="dd-opt" class:mono id={optId(i)} role="option" tabindex="-1" aria-selected={i === cur} aria-disabled={o.disabled || undefined}
          class:active={i === active} onpointerenter={() => { if (!o.disabled) active = i }} onpointerdown={(e) => e.preventDefault()} onclick={() => pick(i)}>
          {#if option}{@render option(o, false)}{:else}<span class="od-field od-fill"><span class="od-truncate">{o.label}</span>{#if o.sub}<span class="sub">{o.sub}</span>{/if}</span>{/if}
          {#if i === cur}<Icon name="check" class="i-sm dd-tick" />{/if}
        </div>
      {/each}
    </div>
  {/if}
</div>
