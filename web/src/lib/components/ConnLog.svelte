<script>
  // A network's connection log: evrd's steps and the server's lines.
  let { entries = [] } = $props()
  const clock = (t) => new Date(t).toTimeString().slice(0, 8)
</script>

<ol class="conn-log" data-testid="conn-log">
  {#each entries as e, i (i)}
    <li class:bad={e.bad} class:server={e.from === 'server'}><time>{clock(e.at)}</time><span class="from">{e.from}</span><span class="text">{e.text}</span></li>
  {/each}
</ol>

<style>
  .conn-log { list-style: none; margin: 0; padding: var(--s2) 0; max-height: 360px; overflow-y: auto; border-radius: var(--r-row); background: var(--bg-deep); border: 1px solid var(--line-soft); font-family: var(--font-mono); font-size: var(--fs-xs); }
  .conn-log li { display: grid; grid-template-columns: 64px 52px minmax(0, 1fr); gap: var(--s2); padding: 2px var(--s3); }
  .conn-log time { color: var(--faint); }
  .conn-log .from { color: var(--muted); }
  .conn-log .server .from { color: var(--cyan); }
  .conn-log .text { overflow-wrap: anywhere; white-space: pre-wrap; }
  .conn-log .bad .text, .conn-log .bad .from { color: var(--signal); }
</style>
