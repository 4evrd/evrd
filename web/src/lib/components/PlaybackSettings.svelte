<script>
  // A network's playback buffers: what an IRC client gets when it attaches.
  // The network's lines per buffer, each buffer's own lines and auto-clear,
  // what's waiting, and clearing — the same settings as *status setbuffer,
  // autoclear and clearbuffer. Changes save at once.
  import { onMount } from 'svelte'
  import Icon from './Icon.svelte'
  import Field from './Field.svelte'
  import Select from './Select.svelte'
  import { api } from '$lib/api.js'
  import { toast, confirmDialog } from '$lib/ui.svelte.js'
  import { t } from '$lib/i18n.svelte.js'

  let { id, name } = $props()
  let pb = $state(null)
  let loadErr = $state('')
  let netLines = $state('')
  let lines = $state({}) // buffer → what's typed
  let busy = $state(false)

  function take(p) {
    pb = p
    netLines = p.lines == null ? '' : String(p.lines)
    lines = Object.fromEntries(p.buffers.map((b) => [b.name, b.lines == null ? '' : String(b.lines)]))
  }
  onMount(async () => {
    try { take(await api.playback(id)) } catch (e) { loadErr = e.message }
  })

  async function apply(call, done) {
    busy = true
    try { take(await call()); if (done) toast(done) } catch (e) { toast(e.message, 'bad') } finally { busy = false }
  }
  // lines: '' goes back to the default; else a whole number 0–max
  function parse(v) {
    v = String(v).trim()
    if (v === '') return null
    const n = Number(v)
    return Number.isInteger(n) && n >= 0 && n <= pb.max ? n : undefined
  }
  function saveNetLines() {
    const v = parse(netLines)
    if (v === undefined) { toast(t('networks.playback.badLines', { max: pb.max }), 'bad'); netLines = pb.lines == null ? '' : String(pb.lines); return }
    if (v === pb.lines) return
    apply(() => api.setPlayback(id, { buffers: [''], lines: v }), t('networks.playback.savedNetwork', { name }))
  }
  function saveLines(b) {
    const v = parse(lines[b.name])
    if (v === undefined) { toast(t('networks.playback.badLines', { max: pb.max }), 'bad'); lines[b.name] = b.lines == null ? '' : String(b.lines); return }
    if (v === b.lines) return
    apply(() => api.setPlayback(id, { buffers: [b.name], lines: v }), t('networks.playback.saved', { buffer: b.name }))
  }
  const onEnter = (e) => { if (e.key === 'Enter') { e.preventDefault(); e.currentTarget.blur() } }
  const clearMode = (b) => (b.autoClear == null ? 'default' : b.autoClear ? 'on' : 'off')
  function setClear(b, v) {
    apply(() => api.setPlayback(id, { buffers: [b.name], autoClear: v === 'default' ? null : v === 'on' }), t('networks.playback.saved', { buffer: b.name }))
  }
  const clearOne = (b) => apply(() => api.clearPlayback(id, [b.name]), t('networks.playback.cleared', { buffer: b.name }))
  async function clearAll() {
    if (!(await confirmDialog({ title: t('networks.playback.clearAllConfirm', { name }), body: t('networks.playback.clearAllBody'), action: t('networks.playback.clearAll') }))) return
    apply(() => api.clearPlayback(id, []), t('networks.playback.clearedAll', { name }))
  }
  const when = (s) => new Date(s * 1000).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
</script>

<section class="card" id="playback" aria-labelledby="playback-h" data-testid="playback">
  <div class="panel-grid">
    <div class="od-stack"><h2 id="playback-h">{t('networks.section.playback')}</h2><p class="desc">{t('networks.playback.intro')}</p></div>
    {#if loadErr}
      <div class="callout bad" role="alert"><Icon name="alert" /><span>{loadErr}</span></div>
    {:else if !pb}
      <div class="skel" style="height:120px"></div>
    {:else}
      <div class="od-stack" style="--od-gap:12px">
        <Field id="pb-lines" label={t('networks.playback.lines')} help={t('networks.playback.linesHelp', { lines: pb.defaults.lines })}>
          <input class="input mono lines" id="pb-lines" inputmode="numeric" placeholder={String(pb.defaults.lines)} bind:value={netLines} onblur={saveNetLines} onkeydown={onEnter} disabled={busy} />
        </Field>
        {#if pb.buffers.length}
          <ul class="pb-list" aria-label={t('networks.playback.buffers')}>
            {#each pb.buffers as b (b.name)}
              <li class="pb-row" data-testid="pb-row">
                <div class="pb-head">
                  <span class="mono name">{b.name}</span>
                  <span class="chip" class:on={b.waiting > 0}><span data-testid="pb-waiting">{b.waiting}</span> {t('networks.playback.waitingWord')}</span>
                </div>
                <div class="pb-ctl">
                  <label class="lines-field">
                    <input class="input mono lines" aria-label={t('networks.playback.linesFor', { buffer: b.name })} inputmode="numeric" placeholder={String(pb.playsLines)} bind:value={lines[b.name]} onblur={() => saveLines(b)} onkeydown={onEnter} disabled={busy} />
                    <span class="faint">{t('networks.playback.linesWord')}</span>
                  </label>
                  <div class="mode"><Select id="pb-ac-{b.name}" value={clearMode(b)} disabled={busy} onchange={(v) => setClear(b, v)} options={[
                    { value: 'default', label: t(b.autoClearDefault ? 'networks.playback.defaultOn' : 'networks.playback.defaultOff') },
                    { value: 'on', label: t('networks.playback.onlyNew') },
                    { value: 'off', label: t('networks.playback.lastLines') },
                  ]} /></div>
                  <button class="btn btn-sm btn-ghost" type="button" disabled={busy || !b.waiting} title={t('networks.playback.clearHelp')} aria-label={t('networks.playback.clearBuffer', { buffer: b.name })} onclick={() => clearOne(b)}>{t('networks.playback.clear')}</button>
                </div>
                {#if b.clearedAt}<span class="faint cleared">{t('networks.playback.clearedAt', { when: when(b.clearedAt) })}</span>{/if}
              </li>
            {/each}
          </ul>
          <div class="od-cluster foot">
            <p class="desc">{t('networks.playback.historyKept')}</p>
            <button class="btn btn-sm" type="button" disabled={busy} onclick={clearAll}><Icon name="trash" />{t('networks.playback.clearAll')}</button>
          </div>
        {:else}
          <p class="desc">{t('networks.playback.none')}</p>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  .panel-grid { display: grid; grid-template-columns: minmax(0, 240px) minmax(0, 1fr); gap: var(--s7); }
  .panel-grid :global(.od-stack) { --od-gap: 6px; }
  .pb-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--s2); }
  .pb-row { display: grid; gap: var(--s2); padding: var(--s3); border-radius: var(--r-row); background: var(--row); }
  .pb-head { display: flex; justify-content: space-between; align-items: baseline; gap: var(--s3); }
  .name { overflow-wrap: anywhere; }
  .chip { flex: none; }
  .chip.on { color: var(--fg); }
  .pb-ctl { display: flex; flex-wrap: wrap; align-items: center; gap: var(--s2) var(--s3); }
  .lines-field { display: inline-flex; align-items: center; gap: 8px; font-size: var(--fs-sm); }
  .lines { height: 36px; width: 76px; }
  .mode { min-width: 200px; flex: 1 1 200px; max-width: 280px; }
  #pb-lines { max-width: 120px; }
  .cleared { font-size: var(--fs-xs); }
  .foot { justify-content: space-between; flex-wrap: wrap; }
  @media (max-width: 959px) { .panel-grid { grid-template-columns: minmax(0, 1fr); gap: var(--s4); } }
</style>
