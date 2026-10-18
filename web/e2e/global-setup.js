import { execSync } from 'node:child_process'

// Ensure the e2e accounts exist (a user and an admin). Best-effort: they're
// created with evrctl inside the running evrd container; if they already exist
// this is a no-op — the tests just need them present.
//
// E2E_EVRCTL overrides how evrctl is run (CI runs the production image, which
// ships the evrctl binary instead of the Go toolchain).
const EVRCTL = process.env.E2E_EVRCTL ||
  'docker compose -f ../docker-compose.dev.yml exec -T evrd go run ./cmd/evrctl -config deploy/evrd.dev.conf'

export default function globalSetup() {
  for (const args of ['user add e2e e2epassword1', 'user add -admin e2eadmin e2eadminpass']) {
    try {
      execSync(`${EVRCTL} ${args}`, { stdio: 'ignore' })
    } catch {
      // already exists, or seeded out-of-band — fine
    }
  }
}
