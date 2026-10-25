<script>
  // A network's uptime as a band of slots: connected the whole slot, part
  // of it, not at all, or nothing reported (switched off, or evrd wasn't
  // running), or connected while the server was split from the rest of the
  // network. Hovering a slot shows how much of it was connected (and split).
  //
  // points: [fraction connected | null …]; split: [fraction split | null …]
  // label:  (i) => what slot i covers; axis: [[i, text]]
  import { slotState, counted } from '$lib/health.js'
  import { t } from '$lib/i18n.svelte.js'

  let { points = [], split = [], label = (i) => String(i), axis = [] } = $props()

  const n = $derived(points.length)
  const tally = $derived(counted(points, split))
  const STATES = $derived([['up', t('health.stateUp')], ['part', t('health.statePart')], ['split', t('health.stateSplit')], ['down', t('health.stateDown')], ['none', t('health.stateNone')]])
  // the split state only shows in the legend when there was one
  const shown = $derived(STATES.filter(([k]) => k !== 'split' || tally.split > 0))
  const name = $derived(Object.fromEntries(STATES))
  const pct = (v) => (v == null ? '—' : Math.round(v * 1000) / 10 + '%')

  let hover = $state(null)
  function move(e) {
    const r = e.currentTarget.getBoundingClientRect()
    hover = n ? Math.max(0, Math.min(n - 1, Math.floor(((e.clientX - r.left) / r.width) * n))) : null
  }
</script>

<div class="uband" data-testid="uptime-band">
  <div class="legend">
    {#each shown as [k, l]}<span><i class="sw {k}"></i>{l}<b>{tally[k]}</b></span>{/each}
  </div>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="band" class:dense={n > 48} style="--n:{n}" onpointermove={move} onpointerleave={() => (hover = null)} aria-hidden="true">
    {#each points as v, i}<span class="cell {slotState(v, split[i])}" class:on={hover === i}></span>{/each}
    {#if hover != null}
      {@const left = ((hover + 0.5) / n) * 100}
      <div class="tip" class:flip={left > 60} style="left:{left}%" data-testid="uptime-readout">
        <div class="readout-h">{label(hover)}</div>
        <div class="readout-row"><i class="sw {slotState(points[hover])}"></i><span>{name[slotState(points[hover])]}</span><b>{pct(points[hover])}</b></div>
        {#if split[hover] >= 0.005 && points[hover] > 0}<div class="readout-row"><i class="sw split"></i><span>{name.split}</span><b>{pct(split[hover])}</b></div>{/if}
      </div>
    {/if}
  </div>
  <div class="axis" aria-hidden="true">{#each axis as [i, text]}<span style="left:{(i / n) * 100}%">{text}</span>{/each}</div>
</div>

<style>
  .uband { display: grid; gap: var(--s2); }
  .legend { display: flex; flex-wrap: wrap; gap: var(--s2) var(--s4); font-size: var(--fs-xs); color: var(--muted); }
  .legend span { display: inline-flex; align-items: center; gap: 6px; }
  .legend b { font-family: var(--font-mono); font-weight: 500; color: var(--fg); }
  .sw { width: 10px; height: 10px; border-radius: 3px; flex: none; }
  .up { background: var(--ok); }
  .part { background: var(--warn); }
  .down { background: var(--bad); }
  /* connected but split from the network: striped, so it reads apart from "partly" without colour */
  .split { background: repeating-linear-gradient(135deg, var(--warn) 0 3px, var(--warn-soft) 3px 6px); }
  .none { background: repeating-linear-gradient(135deg, var(--line) 0 2px, transparent 2px 5px); box-shadow: inset 0 0 0 1px var(--line-soft); }
  .band { position: relative; display: grid; grid-template-columns: repeat(var(--n), minmax(0, 1fr)); gap: 2px; height: 32px; cursor: crosshair; touch-action: pan-y; }
  .band.dense { gap: 1px; }
  .cell { border-radius: 3px; }
  .band.dense .cell { border-radius: 1px; }
  .cell.on { outline: 2px solid var(--fg); outline-offset: 1px; }
  .tip { position: absolute; top: calc(100% + 6px); margin-left: 12px; min-width: 160px; padding: var(--s2) var(--s3); pointer-events: none; z-index: 2;
    background: var(--bg-deep); border: 1px solid var(--line-2); border-radius: 6px; font-size: var(--fs-xs); box-shadow: 0 8px 24px oklch(0% 0 0 / 0.35); }
  .tip.flip { margin-left: 0; transform: translateX(calc(-100% - 12px)); }
  .tip .sw { width: 10px; height: 10px; }
  .axis { position: relative; height: 14px; font-family: var(--font-mono); font-size: 10px; color: var(--faint); }
  .axis span { position: absolute; transform: translateX(-50%); white-space: nowrap; }
  .axis span:first-child { transform: none; }
  .axis span:last-child { transform: translateX(-100%); }
  @media (max-width: 639px) { .axis span:nth-child(even):not(:last-child) { display: none; } }
</style>
