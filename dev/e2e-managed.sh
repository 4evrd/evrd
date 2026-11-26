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
# Then ngircd with Anope (dev/ngircd-anope) for TestManagedNickGuard: an
# account without a services login, given nicks someone registered with
# NickServ, moves off them before joining, is never identified, and
# isn't renamed or killed by NickServ's protection.
#
# Needs docker and go.
set -euo pipefail
cd "$(dirname "$0")/.."

NAME=evrd-e2e-ngircd
ANOPE=evrd-e2e-ngircd-anope
PORT=${NGIRCD_PORT:-16668}
ANOPE_PORT=${NGIRCD_ANOPE_PORT:-16669}
docker build -q -t "$NAME" dev/ngircd >/dev/null
docker build -q -t "$ANOPE" dev/ngircd-anope >/dev/null
docker rm -f "$NAME" "$ANOPE" >/dev/null 2>&1 || true
cleanup() {
	for n in "$NAME" "$ANOPE"; do
		docker logs "$n" 2>&1 | tail -30
		docker rm -f "$n" >/dev/null 2>&1 || true
	done
}
trap cleanup EXIT
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:6667" "$NAME" >/dev/null
docker run -d --name "$ANOPE" -p "127.0.0.1:$ANOPE_PORT:6667" "$ANOPE" >/dev/null
for i in $(seq 1 50); do
	if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then break; fi
	sleep 0.2
done
EVRD_E2E_NGIRCD=127.0.0.1:$PORT go test -count=1 -run 'TestManagedNgircd' -v ./internal/commands/evrd
# services linked: Anope logs it once it's synced with ngircd
for i in $(seq 1 100); do
	if docker logs "$ANOPE" 2>&1 | grep -q 'services.ngircd.test (Services) is done syncing'; then break; fi
	sleep 0.2
done
EVRD_E2E_NGIRCD_ANOPE=127.0.0.1:$ANOPE_PORT go test -count=1 -run 'TestManagedNickGuard' -v ./internal/commands/evrd
