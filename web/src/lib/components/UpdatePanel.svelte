<script>
  // New evrd releases, for admins: the latest release, and installing it.
  // compact: only a notice when there's one to install (Admin overview).
  import { onDestroy } from 'svelte'
  import Icon from '$lib/components/Icon.svelte'
  import { api } from '$lib/api.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { ago } from '$lib/format.js'
  import { t } from '$lib/i18n.svelte.js'

  let { status = $bindable(null), compact = false } = $props()
  let checking = $state(false)
  // the install under way: its version and stage (installing, restarting)
  let target = $state('')
  let stage = $state('')
  let poll

  const u = $derived(status)
  const latest = $derived(u?.latest)

  async function check() {
    checking = true
    try {
      status = await api.admin.checkUpdate()
      if (status.error) toast(t('update.checkFailed', { error: status.error }), 'bad')
    } catch (x) { toast(x.message, 'bad') } finally { checking = false }
  }

  async function install() {
    const v = latest.version
    if (!(await confirmDialog({ title: t('update.installTitle', { version: v }), body: t('update.installBody'), action: t('update.installAction'), danger: false }))) return
    try { status = await api.admin.installUpdate() } catch (x) { toast(x.message, 'bad'); return }
    target = v
    stage = 'installing'
    const started = Date.now()
    clearInterval(poll)
    poll = setInterval(async () => {
      if (Date.now() - started > 5 * 60 * 1000) {
        clearInterval(poll); stage = ''; toast(t('update.timedOut', { version: v }), 'bad'); return
      }
      let s
      try { s = await api.admin.update() } catch { stage = 'restarting'; return } // evrd is restarting
      status = s
      if (s.current === v) {
        clearInterval(poll)
        toast(t('update.done', { version: v }))
        setTimeout(() => location.reload(), 800) // the update's web app
      } else if (s.installError && !s.installing) {
        clearInterval(poll); stage = ''; toast(t('update.failed', { error: s.installError }), 'bad')
      } else if (!s.installing && stage === 'installing') {
        stage = 'restarting'
      }
    }, 1500)
  }
  onDestroy(() => clearInterval(poll))
</script>

{#if u && (!compact || u.available)}
  <div class={compact ? 'callout update-callout' : 'rows update-status'} data-testid="update-status">
    {#if compact}<Icon name="download" />{/if}
    <div class={compact ? 'grow' : 'row'} style={compact ? '' : '--cols:minmax(0,1fr) auto'}>
      <span class="od-field">
        <span class="name">
          {#if u.available}{t('update.available', { version: latest.version })}
          {:else if latest}{t('update.upToDate', { current: u.current })}
          {:else}{t('update.running', { current: u.current })}{/if}
        </span>
        <span class="sub">
          {#if u.available}{t('update.availableBody', { current: u.current })}{#if latest.published}{' · '}{t('update.released', { time: ago(latest.published) })}{/if}{:else if !u.source}{t('update.checkOff')}{:else}{u.checkedAt ? t('update.checked', { time: ago(u.checkedAt) }) : t('update.notChecked')}{/if}
          {#if u.base}{' · '}{t('update.overBase', { base: u.base })}{/if}
          {#if u.error}{' · '}{t('update.checkFailed', { error: u.error })}{/if}
          {#if latest?.url && u.available}{' · '}<a href={latest.url} target="_blank" rel="noopener noreferrer">{t('update.whatsNew')}</a>{/if}
        </span>
        {#if stage === 'installing' || u.installing}<span class="sub" role="status" data-testid="update-progress"><Icon name="refresh" class="spin" /> {t('update.installing', { version: target || u.installing })}</span>
        {:else if stage === 'restarting'}<span class="sub" role="status" data-testid="update-progress"><Icon name="refresh" class="spin" /> {t('update.restarting', { version: target })}</span>{/if}
        {#if u.installError && !stage}<span class="sub bad-text">{t('update.failed', { error: u.installError })}</span>{/if}
        {#if u.available && !u.canInstall && u.why}<span class="sub">{t('update.cant', { why: u.why })}</span>{/if}
        {#if u.available && u.canInstall && u.mode === 'restart' && !compact}<span class="sub">{t('update.modeRestart')}</span>{/if}
      </span>
      <span class="od-cluster">
        {#if !compact && u.source}<button class="btn btn-sm" type="button" onclick={check} disabled={checking || !!stage}><Icon name="refresh" />{t('update.checkNow')}</button>{/if}
        {#if compact && !u.canInstall}<a class="btn btn-sm" href="/admin/settings#g-Updates">{t('update.viewSettings')}</a>{/if}
        {#if u.available && u.canInstall}<button class="btn btn-sm btn-primary" type="button" onclick={install} disabled={!!stage || !!u.installing} data-testid="update-install"><Icon name="download" />{t('update.install', { version: latest.version })}</button>{/if}
      </span>
    </div>
  </div>
{/if}

<style>
  .update-callout { margin-top: var(--s5); align-items: center; }
  .update-callout .grow { flex: 1; display: flex; gap: var(--s3); align-items: center; justify-content: space-between; flex-wrap: wrap; min-width: 0; }
  .update-status { margin-bottom: var(--s3); }
  .bad-text { color: var(--bad); }
  .sub :global(.spin) { animation: spin 1s linear infinite; vertical-align: -2px; }
  @keyframes spin { to { transform: rotate(360deg) } }
</style>
