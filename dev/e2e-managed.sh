#!/usr/bin/env bash
# End-to-end test of managed mode (docs/managed.md) against a real ngircd,
# a server without IRCv3, in Docker:
#
#   dev/e2e-managed.sh
#
# Starts ngircd (dev/ngircd: WEBIRC on, no per-address limit), then runs
# TestManagedNgircd (internal/commands/evrd): evrd in managed mode in front
# of it, an account made through the managed API, a client signing in with
# an access token over SASL OAUTHBEARER, and a classic client connected to
# ngircd directly. It checks the managed user shows with their own address
# (WEBIRC), gets server-time, msgids, echo-message and chathistory from a
# server that has none of them, and that nothing names evrd.
#
# Needs docker and go.
set -euo pipefail
cd "$(dirname "$0")/.."

NAME=evrd-e2e-ngircd
PORT=${NGIRCD_PORT:-16668}
docker build -q -t "$NAME" dev/ngircd >/dev/null
docker rm -f "$NAME" >/dev/null 2>&1 || true
trap 'docker logs "$NAME" 2>&1 | tail -30; docker rm -f "$NAME" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:6667" "$NAME" >/dev/null
for i in $(seq 1 50); do
	if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then break; fi
	sleep 0.2
done
EVRD_E2E_NGIRCD=127.0.0.1:$PORT go test -count=1 -run 'TestManagedNgircd' -v ./internal/commands/evrd
