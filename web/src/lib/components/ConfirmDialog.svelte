<script>
  import { tick } from 'svelte'
  import { dialog } from '$lib/ui.svelte.js'

  let el = $state()
  let typed = $state('')
  let input = $state()
  let cancelBtn = $state()
  const d = $derived(dialog.current)

  $effect(() => {
    if (d && el && !el.open) {
      typed = ''
      el.showModal()
      tick().then(() => (input || cancelBtn)?.focus())
    }
  })

  function done(ok) {
    const cur = dialog.current
    dialog.current = null
    if (el?.open) el.close()
    cur?.resolve(ok)
  }
</script>

<dialog class="modal" aria-labelledby="dlg-title" bind:this={el} onclose={() => dialog.current && done(false)}>
  {#if d}
    <form method="dialog" onsubmit={(e) => { e.preventDefault(); if (!d.requireText || typed.trim() === d.requireText) done(true) }}>
      <div class="modal-body">
        <h2 id="dlg-title">{d.title}</h2>
        <div class="msg">{@html d.body}</div>
        {#if d.requireText}
          <div class="field">
            <label for="dlg-confirm">Type <span class="code">{d.requireText}</span> to confirm</label>
            <input id="dlg-confirm" class="input" autocomplete="off" spellcheck="false" autocapitalize="none" bind:value={typed} bind:this={input} />
          </div>
        {/if}
      </div>
      <div class="modal-foot">
        <!-- type=button: Enter submits with the confirm button, not this one -->
        <button class="btn btn-ghost" type="button" bind:this={cancelBtn} onclick={() => done(false)}>Cancel</button>
        <button class="btn {d.danger ? 'btn-danger-solid' : 'btn-primary'}" value="ok" type="submit" id="dlg-ok"
          disabled={!!d.requireText && typed.trim() !== d.requireText}>{d.confirmLabel}</button>
      </div>
    </form>
  {/if}
</dialog>
