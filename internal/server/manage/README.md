# Optional subscription pool in the existing profile workflow

Enable `SUBSCRIPTION_MANAGER=true`. The existing `/#/profiles` import, edit,
refresh, delete and interval controls remain the only subscription workflow.
There is no separate management page or second source CRUD API. `/manage/`
redirects old bookmarks to `/#/profiles`.

## Boundary and source authority

- The original ProfileStore owns source IDs, URLs, raw subscription bytes,
  traffic metadata, update intervals and its index. Only remote subscriptions
  join the pool; ordinary local profiles retain independent activation.
- Three optional Store callbacks validate a remote source before committing,
  notify after commit and unlock, and transform a pooled base before the
  original overlay/settings pipeline. Nil leaves upstream behavior unchanged.
- Control has optional activation/validation delegates. Ordinary activation
  still uses upstream validation, rollback and restart, serialized with pool
  application so a delayed pool reload cannot take over an ordinary profile.
- The original scheduler remains the only subscription downloader/scheduler.
  Its optional applied-refresh callback prevents restarting an already hot
  applied pooled source. The manager has no subscription fetch timer.
- The existing frontend receives small capability-gated changes in
  `patches/metacubexd-subscription-pool.patch`: membership badges/help, URL edit,
  draft validation, pending-apply feedback and query invalidation. There is no
  new page, sidebar entry, framework or visual redesign.

## Runtime

Source caches and stable profile/source bindings produce one native HTTP
provider per source. The provider feed is a read-only loopback snapshot using
existing authentication, never a second downloader. active.yaml contains
provider references and groups, never inline subscription node entities.
Source URL/metadata/node changes hot update native providers. Source collection
or group structure changes validate and reload configuration in the same core
process; structural reload can briefly affect traffic.

The existing settings/merge overlays compose after the pool base. Ordinary
local profile activation suspends managed application; pooled-source activation
returns to the complete pool. Empty pools/groups reject rather than fall back
to DIRECT. Cached source IDs retain exact exclusions/tags across refreshes.
Bounded native health-check warmup after a new core PID handles a repeater uplink
that becomes ready after initial probes fail; normal selection/lazy checks stay
owned by Mihomo.

Boot migration removes only previously recorded generated profile IDs after
validating the replacement runtime. It does not hide ordinary local profiles or
create replacement source records. Original remote IDs/URLs/caches survive.

## Build and validation

`bash scripts/build-pool-ui.sh` builds the pinned upstream dashboard plus its
small source patch; Node/pnpm are build tools only. The router runs one Go
service and the existing native core, with no new runtime dependencies.

Run `go test ./...`, `go vet ./...`, and
`MIHOMO_TEST_BINARY=/absolute/path/to/mihomo go test -race ./internal/server/manage`.
The native tests exercise live proxied connections through original source
import/edit APIs and automatic recovery after late uplink. Original upstream
tests remain unchanged. Deployment must retain backups and verify original-page
source count, native providers, original settings/rules and actual proxy traffic.
