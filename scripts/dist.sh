#!/bin/sh
# Builds the release archives: evrd_<os>_<arch>.tar.gz for each target, with
# evrd, evrctl, evrd-links, the built web app (web/), LICENSE and README.md, and SHA256
# with BSD-style checksums (what OpenBSD's sha256 -C and install.sh read).
#
#   scripts/dist.sh <version> [os/arch ...]     → dist/
#
# Build the web app first (cd web && pnpm build).
set -eu
export COPYFILE_DISABLE=1 # no macOS ._ files in the archives

version=${1:?usage: scripts/dist.sh <version> [os/arch ...]}
shift
targets=${*:-linux/amd64 linux/arm64 freebsd/amd64 freebsd/arm64 openbsd/amd64 openbsd/arm64 darwin/amd64 darwin/arm64}

cd "$(dirname "$0")/.."
[ -f web/build/index.html ] || { echo "dist: web/build is missing — run pnpm build in web/ first" >&2; exit 1; }
rm -rf dist && mkdir -p dist

ldflags="-s -w -X github.com/4evrd/evrd/internal/version.Version=$version"
for t in $targets; do
	os=${t%/*} arch=${t#*/}
	name=evrd_${os}_${arch}
	stage=dist/$name
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/evrd" ./cmd/evrd
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/evrctl" ./cmd/evrctl
	# no version or commit stamp (-buildvcs=false: Go would write the commit
	# in): evrd-links stays byte-identical between releases unless its code
	# or the Go version changed, so upgrades leave it (and the links) up
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false -ldflags "-s -w" -o "$stage/evrd-links" ./cmd/evrd-links
	if go version -m "$stage/evrd-links" | grep -q 'vcs\.'; then
		echo "dist: evrd-links has build-time stamps — it would change every release" >&2
		exit 1
	fi
	cp -R web/build "$stage/web"
	cp LICENSE README.md "$stage/"
	tar -C dist -czf "dist/$name.tar.gz" "$name"
	rm -rf "$stage"
	echo "dist: $name.tar.gz"
done

cd dist
if command -v sha256sum >/dev/null; then
	sha256sum --tag ./*.tar.gz | sed 's|(\./|(|' >SHA256
else
	shasum -a 256 --tag ./*.tar.gz | sed 's|(\./|(|' >SHA256
fi
cat SHA256
