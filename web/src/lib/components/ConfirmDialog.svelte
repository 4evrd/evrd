<script>
  // The one confirm dialog (ui.svelte.js confirmDialog opens it).
  import { tick } from 'svelte'
  import Icon from './Icon.svelte'
  import { dialog } from '$lib/ui.svelte.js'

  let el = $state()
  let typed = $state('')
  let input = $state(), cancelBtn = $state()
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

<dialog class="confirm" aria-labelledby="cf-t" bind:this={el} onclose={() => dialog.current && done(false)}>
  {#if d}
    <form method="dialog" onsubmit={(e) => { e.preventDefault(); if (!d.typed || typed === d.typed) done(true) }}>
      <div class="confirm-body">
        {#if d.danger}<span class="ic"><Icon name="alert" /></span>{/if}
        <h2 id="cf-t">{d.title}</h2>
        <p>{d.body}</p>
        {#if d.typed}
          <div class="field">
            <label for="cf-in">Type <span class="code">{d.typed}</span> to confirm</label>
            <input class="input mono" id="cf-in" autocomplete="off" spellcheck="false" bind:value={typed} bind:this={input} />
          </div>
        {/if}
      </div>
      <div class="confirm-foot">
        <button class="btn btn-ghost" type="button" bind:this={cancelBtn} onclick={() => done(false)}>Cancel</button>
        <button class="btn {d.danger ? 'btn-danger' : 'btn-primary'}" type="submit" id="dlg-ok" disabled={!!d.typed && typed !== d.typed}>{d.action}</button>
      </div>
    </form>
  {/if}
</dialog>
