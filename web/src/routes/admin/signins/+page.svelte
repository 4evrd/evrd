<script>
  // IRC sign-ins, newest first: the shape of what each client sent in
  // PASS — never what it sent — whose account it was and how it went, so
  // an admin can tell someone what to put in their client (#143).
  import { untrack } from 'svelte'
  import { page } from '$app/state'
  import Icon from '$lib/components/Icon.svelte'
  import { api } from '$lib/api.js'
  import { session } from '$lib/session.svelte.js'
  import { ago } from '$lib/format.js'
  import { i18n, t } from '$lib/i18n.svelte.js'

  const isAdmin = $derived(session.me?.role === 'admin')
  // ?user= one account's, or "unknown": the ones no account was found for
  const user = $derived(page.url.searchParams.get('user') || '')
  let only = $state('all') // all | wrong
  let d = $state(null)
  let err = $state('')
  async function load() {
    try { d = await api.admin.ircSignIns(user || undefined); err = '' } catch (e) { err = e.message }
  }
  $effect(() => {
    user // load again when the filter changes
    if (isAdmin) untrack(load)
  })
  const rows = $derived((d?.signIns || []).filter((x) => only === 'all' || (!x.full && x.format !== 'sasl')))
  const FILTERS = $derived([['all', t('admin.signins.all')], ['wrong', t('admin.signins.wrong')]])
  // ok and links allowed are fine; links waiting, second factors and
  // turned-away links are neither; anything else was refused
  const tone = (r) => (r === 'ok' || r === 'link_allowed' ? 'ok' : ['link', 'second_factor', 'link_denied'].includes(r) ? 'warn' : 'bad')
  const result = (r) => { const k = 'admin.signins.result.' + r, s = t(k); return s === k ? r : s }
  const when = (u) => new Date(u * 1000).toLocaleString(i18n.intl)
  const sent = (f) => (f === 'nothing' ? t('admin.signins.nothing') : f === 'sasl' ? 'SASL' : f)
</script>

<svelte:head><title>{t('admin.signins.pageTitle')}</title></svelte:head>

{#if !isAdmin}
  <div class="callout bad" role="alert"><Icon name="lock" /><span><b>{t('admin.adminsOnly')}</b> {t('admin.signins.noAccess')}</span></div>
{:else}
  <div class="page-head enter">
    <div class="titles">
      <h1>{t('admin.signins.heading')}</h1>
      <p>{t('admin.signins.intro', { full: '<username>/<network>:<password>' })}</p>
    </div>
    <div class="tabs status-tabs" role="tablist" aria-label={t('admin.signins.show')}>
      {#each FILTERS as [k, l]}<button class="tab" role="tab" aria-selected={only === k} data-testid="signins-{k}" onclick={() => (only = k)}>{l}</button>{/each}
    </div>
  </div>

  {#if d?.user || user === 'unknown'}
    <div class="callout enter" role="status" data-testid="signins-only" style="margin-bottom:var(--s5)">
      <Icon name="user" />
      <span class="od-fill">{d?.user ? t('admin.signins.onlyUser', { name: d.user.username }) : t('admin.signins.onlyUnknown')}</span>
      {#if d?.user}<a class="btn btn-ghost btn-sm" href="/admin/users/{d.user.id}">{t('admin.drops.openAccount')}</a>{/if}
      <a class="btn btn-ghost btn-sm" href="/admin/signins">{t('admin.drops.showEveryone')}</a>
    </div>
  {/if}

  {#if err && !d}
    <div class="callout bad" role="alert"><Icon name="alert" /><span>{t('admin.signins.loadFailed', { error: err })}</span></div>
  {:else if !d}
    <div class="card"><div class="skel" style="height:220px"></div></div>
  {:else}
    <section class="card enter" aria-label={t('admin.signins.heading')}>
      {#if !rows.length}
        <p class="desc" data-testid="signins-empty">{t('admin.signins.empty')}</p>
      {:else}
        <div class="table-wrap">
          <table class="top">
            <thead><tr><th scope="col">{t('admin.signins.colWhen')}</th><th scope="col">{t('admin.signins.colAccount')}</th><th scope="col">{t('admin.signins.colFrom')}</th><th scope="col">{t('admin.signins.colSent')}</th><th scope="col">{t('admin.signins.colResult')}</th></tr></thead>
            <tbody>
              {#each rows as x, i (x.at + '/' + x.ip + '/' + i)}
                <tr data-testid="signin-row">
                  <td class="sub" title={when(x.at)}>{ago(x.at)}</td>
                  <td>{#if x.user}<a href="/admin/signins?user={x.userId}">{x.user}</a>{:else}<a class="sub" href="/admin/signins?user=unknown">{t('admin.signins.unknown')}</a>{/if}</td>
                  <td><span class="od-field"><span class="mono">{x.ip}</span><span class="sub">{[x.nick && t('admin.signins.nick', { nick: x.nick }), x.ident && t('admin.signins.ident', { ident: x.ident })].filter(Boolean).join(' · ')}</span></span></td>
                  <td>
                    <span class="od-field">
                      <span class="mono" data-testid="signin-format">{#if x.full}<Icon name="check" class="okc" />{:else if x.format !== 'nothing' && x.format !== 'sasl'}<Icon name="alert" class="warnc" />{/if}{sent(x.format)}</span>
                      {#if x.network}<span class="sub">{t('admin.signins.network', { network: x.network })}</span>{/if}
                      {#if x.passwordLength}<span class="sub" data-testid="signin-match">{x.matched ? (x.matched === 'account' ? t('admin.signins.matchedAccount', { length: x.passwordLength, name: x.user }) : t('admin.signins.matchedDevice', { length: x.passwordLength, name: x.user, device: x.matched })) : x.user ? t('admin.signins.matchedNone', { length: x.passwordLength, name: x.user }) : t('admin.signins.length', { length: x.passwordLength })}</span>{/if}
                    </span>
                  </td>
                  <td><span class="chip {tone(x.result)}">{result(x.result)}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <p class="desc" style="font-size:var(--fs-xs);margin-top:var(--s3)">{t('admin.signins.never')}</p>
      {/if}
    </section>
  {/if}
{/if}

<style>
  .table-wrap { overflow-x: auto; }
  .top { width: 100%; border-collapse: collapse; font-size: var(--fs-sm); }
  .top th { font-size: var(--fs-xs); color: var(--muted); font-weight: 500; text-align: left; padding: var(--s2) var(--s3); white-space: nowrap; }
  .top td { padding: var(--s2) var(--s3); border-top: 1px solid var(--line-soft); vertical-align: top; }
  .top td .od-field { display: inline-flex; }
  .mono { font-family: var(--font-mono); font-size: var(--fs-sm); display: inline-flex; align-items: center; gap: 6px; }
  .mono :global(.okc) { color: var(--ok); }
  .mono :global(.warnc) { color: var(--warn); }
</style>
