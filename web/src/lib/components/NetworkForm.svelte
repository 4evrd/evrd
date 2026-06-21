<script>
  let { onsave, oncancel } = $props()

  let net = $state({
    name: '',
    host: '',
    port: 6697,
    tls: true,
    nick: '',
    username: '',
    realname: '',
    password: '',
    channels: '',
  })

  function submit(e) {
    e.preventDefault()
    onsave({
      ...net,
      channels: net.channels
        .split(',')
        .map((c) => c.trim())
        .filter(Boolean),
    })
  }

  const field =
    'mt-1 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm outline-none focus:border-accent'
  const label = 'text-muted block text-xs'
</script>

<form onsubmit={submit} class="grid grid-cols-1 gap-3 sm:grid-cols-2">
  <div class="sm:col-span-2">
    <label class={label} for="n-name">Network name</label>
    <input id="n-name" class={field} bind:value={net.name} placeholder="Libera.Chat" required />
  </div>

  <div>
    <label class={label} for="n-host">Server</label>
    <input id="n-host" class={field} bind:value={net.host} placeholder="irc.libera.chat" required />
  </div>
  <div class="flex gap-3">
    <div class="flex-1">
      <label class={label} for="n-port">Port</label>
      <input id="n-port" type="number" class={field} bind:value={net.port} />
    </div>
    <label class="mt-5 flex items-center gap-2 text-sm whitespace-nowrap">
      <input type="checkbox" bind:checked={net.tls} class="accent-accent" /> TLS
    </label>
  </div>

  <div>
    <label class={label} for="n-nick">Nick</label>
    <input id="n-nick" class={field} bind:value={net.nick} placeholder="yournick" required />
  </div>
  <div>
    <label class={label} for="n-user">Username</label>
    <input id="n-user" class={field} bind:value={net.username} placeholder="optional" />
  </div>

  <div>
    <label class={label} for="n-real">Real name</label>
    <input id="n-real" class={field} bind:value={net.realname} placeholder="optional" />
  </div>
  <div>
    <label class={label} for="n-pass">Server password</label>
    <input id="n-pass" type="password" class={field} bind:value={net.password} placeholder="optional" />
  </div>

  <div class="sm:col-span-2">
    <label class={label} for="n-chan">Channels</label>
    <input id="n-chan" class={field} bind:value={net.channels} placeholder="#ircforever, #linux" />
  </div>

  <div class="mt-1 flex justify-end gap-2 sm:col-span-2">
    <button
      type="button"
      class="rounded-md border border-border px-3 py-2 text-sm hover:border-accent"
      onclick={oncancel}>Cancel</button
    >
    <button class="rounded-md bg-accent px-3 py-2 text-sm font-medium text-bg">Add network</button>
  </div>
</form>
