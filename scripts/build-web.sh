#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
root=$(pwd)
cd examples/player
# Reuse Booba's versioned, embedded browser assets; no JS bundler required.
# go list reports no directory for a module that is not in the module cache yet
# (fresh CI runners), so download it first and refuse to continue without it.
go mod download nimbleterminal.dev/booba
booba_dir=$(go list -m -f '{{.Dir}}' nimbleterminal.dev/booba)
if [ -z "$booba_dir" ]; then
  echo 'build-web.sh: Booba module directory not found' >&2
  exit 1
fi
mkdir -p "$root/dist"
# Module cache assets are read-only; make prior copies replaceable on rebuild.
chmod -R u+w "$root/dist"
cp -R "$booba_dir/serve/static/booba" "$root/dist/"
cp -R "$booba_dir/serve/static/ntwebterm" "$root/dist/"
chmod -R u+w "$root/dist"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$root/dist/"
go tool ntvideo-assets "$root/dist" --force
cp "$root/web/index.html" "$root/dist/"
GOOS=js GOARCH=wasm go build -mod=readonly -o "$root/dist/app.wasm" .
