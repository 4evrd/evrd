#!/bin/sh
# Runs the tests that need a real ScyllaDB: starts one in Docker, runs the
# history conformance suite (and the two-node cluster test) against it, and
# cleans up. Used by CI; works the same locally:
#
#   dev/ci-scylla.sh
#
# Everything runs in containers on a private network, so it only needs a
# Docker CLI — the workspace is copied in rather than mounted, which also
# works when this script itself runs inside a CI job container.
set -eu

id="evrd-ci-$$"
cleanup() {
	docker rm -f "$id-scylla" "$id-test" >/dev/null 2>&1 || true
	docker network rm "$id" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$id" >/dev/null
docker run -d --name "$id-scylla" --network "$id" scylladb/scylla:6.2 \
	--smp 1 --memory 750M --overprovisioned 1 --developer-mode 1 >/dev/null

echo "waiting for ScyllaDB…"
for _ in $(seq 1 90); do
	if docker exec "$id-scylla" cqlsh -e 'DESCRIBE KEYSPACES' >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 2
done
if [ -z "${ready:-}" ]; then
	docker logs --tail 50 "$id-scylla"
	echo "ScyllaDB didn't start" >&2
	exit 1
fi

docker create --name "$id-test" --network "$id" -w /src \
	-e CGO_ENABLED=0 -e EVRD_TEST_SCYLLA="$id-scylla:9042" \
	golang:1.26 go test -count=1 ./internal/history ./internal/clustertest >/dev/null
# Just the Go sources (not node_modules, .git, build output).
COPYFILE_DISABLE=1 tar --no-xattrs -c --exclude=./web --exclude=./.git --exclude=./tmp --exclude=./bin . | docker cp - "$id-test:/src"
docker start -a "$id-test"
