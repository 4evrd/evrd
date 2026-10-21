<script>
  // Columns per time slot, stacked when there's more than one series (the
  // first at the bottom). Hovering a slot shows its values; the biggest is
  // labelled on its cap. The health page's messages and disconnects.
  //
  // series:    [{ name, color, points: [number …] }]
  // label:     (i) => what slot i covers, for the hover readout
  // axis:      [[i, text]] labels under the slot boundaries
  // fmt:       (v) => a value as text
  import { chartMax } from '$lib/format.js'
  import { stackTotals, peakIndex } from '$lib/health.js'

  let { series = [], label = (i) => String(i), axis = [], fmt = (v) => String(v), testid = undefined } = $props()

  const n = $derived(series[0]?.points.length || 0)
  const totals = $derived(stackTotals(series))
  const max = $derived(chartMax(totals))
  const peak = $derived(peakIndex(totals))
  const pct = (v) => Math.min(100, (v / max) * 100)

  let hover = $state(null)
  function move(e) {
    const r = e.currentTarget.getBoundingClientRect()
    hover = n ? Math.max(0, Math.min(n - 1, Math.floor(((e.clientX - r.left) / r.width) * n))) : null
  }
</script>

<div class="sbars" data-testid={testid}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="plot" onpointermove={move} onpointerleave={() => (hover = null)} aria-hidden="true">
    <span class="ymax">{fmt(max)}</span>
    {#each [0.25, 0.5, 0.75] as f}<span class="grid" style="bottom:{f * 100}%"></span>{/each}
    <div class="cols" class:dense={n > 48} style="--n:{n}">
      {#each totals as tot, i}
        <div class="col" class:on={hover === i}>
          {#if i === peak}<span class="cap" style="bottom:calc({pct(tot)}% + 4px)">{fmt(tot)}</span>{/if}
          <div class="stack" style="height:{pct(tot)}%">
            {#each series as s}{#if s.points[i] > 0}<i style="flex:{s.points[i]};background:{s.color}"></i>{/if}{/each}
          </div>
        </div>
      {/each}
    </div>
    {#if hover != null}
      {@const left = ((hover + 0.5) / n) * 100}
      <div class="tip" class:flip={left > 60} style="left:{left}%" data-testid="slot-readout">
        <div class="readout-h">{label(hover)}</div>
        {#each series as s}<div class="readout-row" class:zero={!s.points[hover]}><i style="--c:{s.color}"></i><span>{s.name}</span><b>{fmt(s.points[hover] || 0)}</b></div>{/each}
      </div>
    {/if}
  </div>
  <div class="axis" aria-hidden="true">{#each axis as [i, text]}<span style="left:{(i / n) * 100}%">{text}</span>{/each}</div>
</div>

<style>
  .sbars { display: grid; gap: var(--s2); }
  .plot { position: relative; height: 160px; padding-top: 18px; border-radius: 10px; background: var(--bg-deep); border: 1px solid var(--line-soft); cursor: crosshair; touch-action: pan-y; }
  .grid { position: absolute; left: 0; right: 0; height: 0; border-top: 1px solid var(--line-soft); pointer-events: none; }
  .ymax { position: absolute; top: 4px; left: 8px; font-family: var(--font-mono); font-size: 10px; color: var(--faint); pointer-events: none; }
  .cols { position: relative; display: grid; grid-template-columns: repeat(var(--n), minmax(0, 1fr)); gap: 2px; height: 100%; padding: 0 4px; }
  .cols.dense { gap: 1px; }
  .col { position: relative; display: flex; align-items: flex-end; justify-content: center; border-radius: 4px 4px 0 0; }
  .col.on { background: var(--row-hover); }
  .stack { display: flex; flex-direction: column-reverse; gap: 2px; width: 100%; max-width: 24px; }
  .stack i { min-height: 2px; }
  .stack i:last-child { border-radius: 4px 4px 0 0; }
  .cap { position: absolute; left: 50%; transform: translateX(-50%); font-family: var(--font-mono); font-size: 10px; color: var(--muted); white-space: nowrap; pointer-events: none; }
  .tip { position: absolute; top: 8px; margin-left: 12px; min-width: 160px; max-width: 240px; padding: var(--s2) var(--s3); pointer-events: none; z-index: 2;
    background: var(--bg-deep); border: 1px solid var(--line-2); border-radius: 6px; font-size: var(--fs-xs); box-shadow: 0 8px 24px oklch(0% 0 0 / 0.35); }
  .tip.flip { margin-left: 0; transform: translateX(calc(-100% - 12px)); }
  .axis { position: relative; height: 14px; font-family: var(--font-mono); font-size: 10px; color: var(--faint); }
  .axis span { position: absolute; transform: translateX(-50%); white-space: nowrap; }
  .axis span:first-child { transform: none; }
  .axis span:last-child { transform: translateX(-100%); }
  @media (max-width: 639px) { .axis span:nth-child(even):not(:last-child) { display: none; } }
</style>
