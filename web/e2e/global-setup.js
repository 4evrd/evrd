import { execSync } from 'node:child_process'

// Ensure the e2e account exists. Best-effort: it's created via evrctl inside the
// running evrd container. If it already exists (or the stack is managed
// elsewhere) this is a no-op — the tests just need the account present.
export default function globalSetup() {
  try {
    execSync(
      'docker compose -f ../docker-compose.dev.yml exec -T evrd ' +
        'go run ./cmd/evrctl -db sqlite:/var/lib/evrd/evrd.db user add e2e e2epass',
      { stdio: 'ignore' },
    )
  } catch {
    // already exists, or seeded out-of-band — fine
  }
}
