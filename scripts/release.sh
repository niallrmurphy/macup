#!/bin/sh
# Build release artefacts into dist/: a universal macOS binary, a tarball, and
# SHA256SUMS. Usage: scripts/release.sh 1.0.0
set -eu
version="${1:?version required, e.g. 1.0.0}"
commit="$(git rev-parse --short HEAD)"
date="$(date -u +%Y-%m-%d)"
ldflags="-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${date}"
rm -rf dist && mkdir -p dist/build
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "dist/build/macup_${arch}" ./cmd/macup
done
lipo -create -output dist/macup dist/build/macup_arm64 dist/build/macup_amd64
rm -rf dist/build
cp README.md CHANGELOG.md LICENSE dist/ 2>/dev/null || true
tarball="macup_${version}_darwin_universal.tar.gz"
( cd dist && tar -czf "$tarball" macup README.md CHANGELOG.md $( [ -f LICENSE ] && echo LICENSE ) )
( cd dist && shasum -a 256 "$tarball" > SHA256SUMS )
echo "built dist/$tarball"
dist/macup version
lipo -info dist/macup
# The Homebrew formula pins the sha256 of GitHub's source tarball for the tag,
# which only exists once the tag is pushed; print it when it is available.
src="https://github.com/niallrmurphy/macup/archive/refs/tags/v${version}.tar.gz"
if sum="$(curl -fsSL "$src" | shasum -a 256 | cut -d' ' -f1)"; then
  echo "formula: url \"$src\""
  echo "formula: sha256 \"$sum\""
else
  echo "formula: push tag v${version} first, then re-run to get the source tarball sha256"
fi
