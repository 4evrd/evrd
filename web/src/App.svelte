<script>
  import { createAuthBloc } from './lib/blocs/auth.bloc.js'
  import { createNetworksBloc } from './lib/blocs/networks.bloc.js'
  import Login from './lib/components/Login.svelte'
  import Shell from './lib/components/Shell.svelte'

  const auth = createAuthBloc()
  const networks = createNetworksBloc()
  const { session } = auth
</script>

{#if $session}
  <Shell {auth} {networks} />
{:else}
  <Login {auth} />
{/if}
