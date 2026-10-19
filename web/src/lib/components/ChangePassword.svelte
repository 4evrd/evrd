<script>
  // The password form, used on the account page and as the forced step after
  // signing in with a temporary password.
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import { api } from '$lib/api.js'
  import { refreshMe } from '$lib/session.svelte.js'
  import { toast } from '$lib/ui.svelte.js'
  import { check } from '$lib/validate.js'
  import { t } from '$lib/i18n.svelte.js'

  let { username = '', ondone = null } = $props()
  let cur = $state(''), next = $state(''), rep = $state('')
  let errs = $state({})
  let summary = $state([])
  let busy = $state(false)

  const v = { 's-cur': () => check('required', cur), 's-new': () => check('password', next) || (next && next === cur ? t('shell.samePassword') : ''), 's-rep': () => check('match', rep, next) }
  const label = $derived({ 's-cur': t('shell.currentPassword'), 's-new': t('shell.newPassword'), 's-rep': t('shell.repeatPassword') })
  // Errors appear on blur but clear while typing: clearing one on blur would
  // shift the layout under a click on the submit button.
  const blur = (id, val) => () => { if (val()) errs[id] = v[id]() }
  const input = (id) => () => { if (errs[id]) errs[id] = v[id]() }

  async function submit(e) {
    e.preventDefault()
    for (const id in v) errs[id] = v[id]()
    summary = Object.entries(errs).filter(([, m]) => m)
    if (summary.length) { document.getElementById(summary.length > 1 ? 'pw-summary' : summary[0][0])?.focus(); return }
    busy = true
    try {
      await api.changePassword(cur, next)
      cur = ''; next = ''; rep = ''; errs = {}
      await refreshMe()
      toast(t('shell.passwordChanged'))
      ondone?.()
    } catch (err) {
      if (err.status === 422 && /current/i.test(err.message)) { errs['s-cur'] = t('shell.wrongPassword'); document.getElementById('s-cur')?.focus() }
      else { errs['s-new'] = err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.'; document.getElementById('s-new')?.focus() }
    } finally { busy = false }
  }
</script>

<form class="form" novalidate onsubmit={submit} autocomplete="on">
  <input type="text" name="username" autocomplete="username" value={username} hidden readonly />
  <div class="form-grid">
    <div class="err-summary span-2" class:show={summary.length > 1} role="alert" id="pw-summary" tabindex="-1">
      {#if summary.length > 1}<strong>{t('shell.fixThings', { count: summary.length })}</strong>
        <ul>{#each summary as [id, m]}<li><a href="#{id}" onclick={(e) => { e.preventDefault(); document.getElementById(id)?.focus() }}>{label[id]}</a> — {m}</li>{/each}</ul>{/if}
    </div>
    <Field id="s-cur" label={label['s-cur']} required err={errs['s-cur']} span>
      <input class="input" id="s-cur" type="password" autocomplete="current-password" bind:value={cur} onblur={blur('s-cur', () => cur)} oninput={input('s-cur')} aria-invalid={!!errs['s-cur']} />
    </Field>
    <Field id="s-new" label={label['s-new']} required help={t('shell.newPasswordHelp')} err={errs['s-new']}>
      <input class="input" id="s-new" type="password" autocomplete="new-password" bind:value={next} onblur={blur('s-new', () => next)} oninput={input('s-new')} aria-invalid={!!errs['s-new']} />
    </Field>
    <Field id="s-rep" label={label['s-rep']} required err={errs['s-rep']}>
      <input class="input" id="s-rep" type="password" autocomplete="new-password" bind:value={rep} onblur={blur('s-rep', () => rep)} oninput={input('s-rep')} aria-invalid={!!errs['s-rep']} />
    </Field>
  </div>
  <div class="form-foot" style="display:flex;justify-content:flex-end"><button class="btn btn-primary" type="submit" disabled={busy}>
    {#if busy}<span class="spinner" aria-hidden="true"></span>{t('common.saving')}{:else}<Icon name="lock" />{t('shell.changePassword')}{/if}
  </button></div>
</form>
