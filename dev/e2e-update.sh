#!/usr/bin/env bash
# End-to-end test of installing an update from the web app, in Docker.
#
#   dev/e2e-update.sh
#
# Builds evrd 0.1.0 as an image, and a 0.2.0 release (scripts/dist.sh) that a
# fake release feed serves. Then, against the real images (evrd, evrd-links,
# the web app's nginx) and an ergo IRC server:
#
#   1. exec: evrd runs as the image's user. An admin clicks Install in the
#      web app; evrd downloads and checks 0.2.0 and execs it in its own
#      process — the container doesn't restart, the IRC connection (held by
#      evrd-links) doesn't drop, the web container serves 0.2.0's web app,
#      evrctl is 0.2.0, and a container restart still runs 0.2.0.
#   2. stale: the container is recreated from a 0.3.0 image: 0.3.0 runs and
#      the update is cleared.
#   3. restart: evrd starts as root and drops to evrd (user evrd), so it
#      can't exec into the update: it exits and Docker's restart policy
#      starts it again, into 0.2.0.
#
# Needs docker, pnpm (for the web build), python3 and the web app's
# Playwright (make e2e-setup).
set -euo pipefail
cd "$(dirname "$0")/.."

P=evrdup
WEB_PORT=${WEB_PORT:-18080}
IRC_PORT=${IRC_PORT:-16667}
BASE=http://localhost:$WEB_PORT
PASS=update-e2e-Passw0rd
arch=$(docker info --format '{{.Architecture}}')
case $arch in aarch64 | arm64) arch=arm64 ;; x86_64 | amd64) arch=amd64 ;; esac
work=$(mktemp -d)

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*" >&2; docker logs --tail 40 $P-evrd >&2 || true; exit 1; }
ok() { printf '\033[32mok\033[0m %s\n' "$*"; }

teardown() {
	docker rm -f $P-evrd $P-links $P-web $P-feed $P-ircd >/dev/null 2>&1 || true
	docker volume rm $P-data $P-sock >/dev/null 2>&1 || true
	docker network rm $P >/dev/null 2>&1 || true
}
cleanup() {
	[ -n "${watcher:-}" ] && kill "$watcher" 2>/dev/null || true
	[ -n "${KEEP:-}" ] && { echo "KEEP: left $P-* running, files in $work"; return; }
	teardown
	rm -rf "$work" web/build/update-marker.txt
}
trap cleanup EXIT
teardown

say "images: evrd 0.1.0 and 0.3.0, the web app"
docker build -q --build-arg VERSION=0.1.0 -t $P/evrd:0.1.0 . >/dev/null
docker build -q --build-arg VERSION=0.3.0 -t $P/evrd:0.3.0 . >/dev/null
docker build -q -t $P/web web >/dev/null
ok "built"

say "the 0.2.0 release (linux/$arch) and its feed"
(cd web && pnpm -s build >/dev/null)
echo "web app 0.2.0" >web/build/update-marker.txt # tells its web app from the image's
scripts/dist.sh 0.2.0 "linux/$arch" >/dev/null
rm web/build/update-marker.txt
mkdir -p "$work/feed"
cp dist/* "$work/feed/"
cat >"$work/feed/latest" <<EOF
{"tag_name": "v0.2.0", "html_url": "http://$P-feed/notes", "body": "The e2e release.",
 "published_at": "2026-11-13T00:00:00Z", "draft": false, "prerelease": false,
 "assets": [
  {"name": "evrd_linux_$arch.tar.gz", "browser_download_url": "http://$P-feed/evrd_linux_$arch.tar.gz"},
  {"name": "SHA256", "browser_download_url": "http://$P-feed/SHA256"}]}
EOF
ok "$(cat "$work/feed/SHA256")"

cat >"$work/evrd.conf" <<EOF
listen irc+insecure://:6667
api :8098
db sqlite:/var/lib/evrd/evrd.db
history pebble:/var/lib/evrd/history
public_url $BASE
irc_host localhost
password_login yes
cookie_secure no
network_directory off
links_socket /run/evrd-links/links.sock
update_check http://$P-feed/latest
EOF
cp "$work/evrd.conf" "$work/evrd-root.conf"
echo "user evrd" >>"$work/evrd-root.conf"

# start brings up the stack with evrd from image $1, as user $2 (default:
# the image's), with config $3.
start() {
	local image=$1 user=${2:-} conf=${3:-$work/evrd.conf}
	docker network create $P >/dev/null
	docker volume create $P-data >/dev/null
	docker volume create $P-sock >/dev/null
	docker run -d --name $P-ircd --network $P -p 127.0.0.1:$IRC_PORT:6667 \
		-e ERGO__SERVER__IP_LIMITS__COUNT=false -e ERGO__SERVER__IP_LIMITS__THROTTLE=false \
		ghcr.io/ergochat/ergo:stable >/dev/null
	docker run -d --name $P-feed --network $P -v "$work/feed:/usr/share/nginx/html:ro" nginx:1.27-alpine >/dev/null
	# the holder, whose network namespace evrd joins so connections outlive
	# evrd's container (docs/configuration.md)
	docker run -d --name $P-links --network $P -v $P-sock:/run/evrd-links \
		--entrypoint evrd-links $P/evrd:0.1.0 -socket /run/evrd-links/links.sock >/dev/null
	run_evrd "$image" "$user" "$conf"
	docker run -d --name $P-web --network $P -e EVRD_API=$P-links:8098 -p 127.0.0.1:$WEB_PORT:80 $P/web >/dev/null
	wait_api
}
run_evrd() {
	local image=$1 user=$2 conf=$3
	docker run -d --name $P-evrd --network container:$P-links --restart unless-stopped \
		${user:+--user "$user"} -v $P-data:/var/lib/evrd -v $P-sock:/run/evrd-links \
		-v "$conf:/etc/evrd/evrd.conf:ro" -e EVRD_CONFIG=/etc/evrd/evrd.conf "$image" >/dev/null
}
wait_api() {
	for _ in $(seq 120); do
		curl -sf "$BASE/api/config" >/dev/null 2>&1 && return 0
		sleep 0.5
	done
	fail "evrd didn't come up"
}

jar=$work/cookies
api() { # api METHOD PATH [JSON]
	curl -sS -b "$jar" -c "$jar" -X "$1" -H 'Content-Type: application/json' \
		${3:+--data "$3"} "$BASE$2"
}
field() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval("d"+sys.argv[1]))' "$1"; }
running() { api GET /api/admin/update | field '["current"]'; }
wait_version() {
	for _ in $(seq 240); do
		[ "$(running 2>/dev/null)" = "$1" ] && return 0
		sleep 0.5
	done
	fail "evrd isn't running $1"
}
signin() {
	docker exec $P-evrd evrctl -config /etc/evrd/evrd.conf user add -admin root "$PASS" >/dev/null
	rm -f "$jar"
	api POST /auth/password "{\"username\":\"root\",\"password\":\"$PASS\"}" >/dev/null
}

# watch_irc keeps an IRC client in #up on ergo and logs what it sees: the
# bouncer's nick must join once and never quit.
watch_irc() {
	python3 - "$IRC_PORT" "$work/irc.log" <<'PY' &
import socket, sys, time
log = open(sys.argv[2], "w", buffering=1)
for _ in range(120):  # ergo may still be starting
    try:
        s = socket.create_connection(("127.0.0.1", int(sys.argv[1])))
        s.settimeout(None)
        s.sendall(b"PING :up\r\n")
        if s.recv(1):
            break
    except OSError:
        time.sleep(0.5)
s.sendall(b"NICK watcher\r\nUSER w 0 * :w\r\nJOIN #up\r\n")
buf = b""
while True:
    d = s.recv(4096)
    if not d:
        break
    buf += d
    while b"\r\n" in buf:
        l, buf = buf.split(b"\r\n", 1)
        if l.startswith(b"PING"):
            s.sendall(b"PONG" + l[4:] + b"\r\n")
        log.write(time.strftime("%H:%M:%S ") + l.decode(errors="replace") + "\n")
PY
	watcher=$!
}
joins() { [ -f "$work/irc.log" ] || { echo 0; return; }; grep -c ':updbot![^ ]* JOIN' "$work/irc.log" || true; }
quits() { [ -f "$work/irc.log" ] || { echo 0; return; }; grep -cE ':updbot![^ ]* (QUIT|PART)' "$work/irc.log" || true; }
add_network() {
	api POST /api/networks "{\"name\":\"ergo\",\"host\":\"$P-ircd\",\"port\":6667,\"tls\":false,\"nick\":\"updbot\",\"channels\":[\"#up\"]}" >"$work/network.json"
	local id
	id=$(field '["id"]' <"$work/network.json")
	# a new account is this node's within seconds (its lease)
	for _ in $(seq 60); do
		api POST "/api/networks/$id/connect" | has '"status"' && break
		sleep 0.5
	done
	for _ in $(seq 60); do
		[ "$(joins)" -ge 1 ] && return 0
		sleep 0.5
	done
	fail "the bouncer never joined #up"
}
started() { docker inspect -f '{{.State.StartedAt}} {{.RestartCount}}' $P-evrd; }
marker() { curl -s "$BASE/update-marker.txt"; }
# has STR... : grep -q on a whole captured output (piping into grep -q would
# SIGPIPE the writer, and pipefail fails it)
has() { local out; out=$(cat); grep -q "$1" <<<"$out"; }

# --- 1. exec ---------------------------------------------------------------
say "1. exec: install from the web app"
start $P/evrd:0.1.0
signin
watch_irc
add_network
ok "0.1.0 up, updbot in #up"

st=$(api POST /api/admin/update/check)
[ "$(echo "$st" | field '["available"]')" = True ] || fail "no update offered: $st"
[ "$(echo "$st" | field '["mode"]')" = exec ] || fail "mode: $st"
ok "0.2.0 offered (mode exec)"
marker | has "web app 0.2.0" && fail "the image's web app has the marker"
before=$(started)

WEB_BASE=$BASE ADMIN=root ADMIN_PASS=$PASS SHOT="${SHOT_DIR:-$work}/update.png" \
	node dev/e2e-update-ui.mjs || fail "the web app's install"
ok "clicked Install; the web app reloaded on 0.2.0"

wait_version 0.2.0
st=$(api GET /api/admin/update)
[ "$(echo "$st" | field '["base"]')" = 0.1.0 ] || fail "base: $st"
[ "$(echo "$st" | field '["available"]')" = False ] || fail "still offered: $st"
[ "$(started)" = "$before" ] || fail "the container restarted: $before → $(started)"
ok "running 0.2.0 over 0.1.0, same container ($before)"
docker logs $P-evrd 2>&1 | has "restarting into the update" || fail "no exec in the log"
[ "$(docker exec $P-evrd evrctl version)" = "evrctl 0.2.0" ] || fail "evrctl: $(docker exec $P-evrd evrctl version)"
ok "evrctl is 0.2.0 too"
marker | has "web app 0.2.0" || fail "the web container serves the old web app: $(marker | head -c 200)"
ok "the web container serves 0.2.0's web app"
sleep 2
[ "$(joins)" = 1 ] && [ "$(quits)" = 0 ] || fail "the IRC connection dropped: $(joins) joins, $(quits) quits"
ok "updbot stayed on IRC"

docker restart $P-evrd >/dev/null
wait_api
wait_version 0.2.0
sleep 2
[ "$(joins)" = 1 ] && [ "$(quits)" = 0 ] || fail "the IRC connection dropped on the restart"
ok "a container restart runs 0.2.0 again, still on IRC"

# --- 2. stale --------------------------------------------------------------
say "2. stale: a newer image replaces the update"
docker rm -f $P-evrd >/dev/null
run_evrd $P/evrd:0.3.0 "" "$work/evrd.conf"
wait_api
wait_version 0.3.0
docker logs $P-evrd 2>&1 | has "clearing it" || fail "the stale update wasn't cleared"
docker run --rm -v $P-data:/d alpine ls /d/updates | has 0.2.0 && fail "0.2.0 is still there"
ok "0.3.0 runs, the 0.2.0 update is cleared"
kill "$watcher" 2>/dev/null || true
watcher=
teardown

# --- 3. restart ------------------------------------------------------------
say "3. restart: evrd drops to a user, Docker restarts it"
start $P/evrd:0.1.0 0:0 "$work/evrd-root.conf"
signin
st=$(api POST /api/admin/update/check)
[ "$(echo "$st" | field '["mode"]')" = restart ] || fail "mode: $st"
ok "0.2.0 offered (mode restart)"
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -X POST "$BASE/api/admin/update/install")
[ "$code" = 202 ] || fail "install: $code"
wait_version 0.2.0
[ "$(docker inspect -f '{{.RestartCount}}' $P-evrd)" = 1 ] || fail "restarts: $(docker inspect -f '{{.RestartCount}}' $P-evrd)"
docker logs $P-evrd 2>&1 | has "exiting for the container" || fail "no exit in the log"
docker exec $P-evrd ps -o user,args | has "^evrd .*updates/0.2.0/evrd" || fail "not the update, as evrd: $(docker exec $P-evrd ps -o user,args)"
ok "Docker restarted it once, into 0.2.0, running as evrd"

say "all passed"
