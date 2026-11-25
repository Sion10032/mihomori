#!/bin/bash
# Build the pinned dashboard with the optional subscription-pool UI patch.
# Runtime remains a single Go binary; Node and pnpm are build tools only.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/versions.env"
POOL_UI_WORK="$(mktemp -d)"
trap 'rm -rf "$POOL_UI_WORK"' EXIT
git clone --quiet --depth 1 --branch "$METACUBEXD_VERSION" \
  https://github.com/MetaCubeX/metacubexd.git "$POOL_UI_WORK/source"
git -C "$POOL_UI_WORK/source" apply "$ROOT_DIR/patches/metacubexd-subscription-pool.patch"
cd "$POOL_UI_WORK/source"
# pnpm 11 requires an explicit allow-list for dependency build scripts.
# Keep the setting in this disposable checkout, not the upstream lockfile.
node --input-type=module - <<'JS'
import fs from 'node:fs'
const file = 'pnpm-workspace.yaml'
fs.appendFileSync(file, '\nallowBuilds:\n  "@parcel/watcher": true\n  esbuild: true\n  meta-json-schema: true\n  vue-demi: true\n')
JS
HUSKY=0 pnpm --config.verifyDepsBeforeRun=false install --filter @metacubexd/ui... --frozen-lockfile
pnpm --config.verifyDepsBeforeRun=false --filter @metacubexd/ui generate
rm -rf "$ROOT_DIR/internal/server/static/web"
mkdir -p "$ROOT_DIR/internal/server/static/web"
cp -R packages/ui/.output/public/. "$ROOT_DIR/internal/server/static/web/"
touch "$ROOT_DIR/internal/server/static/web/.gitkeep"
