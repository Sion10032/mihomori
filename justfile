# mihomori — task runner. Run `just` (or `just --list`) to see every recipe.
set shell := ["bash", "-euo", "pipefail", "-c"]

# List available recipes.
default:
    @just --list

# ── Build ──────────────────────────────────────────────────────────────────

# Compile every package (server + TUI). Writes no binaries.
build:
    go build ./...

# Build the server binary into bin/ (trimpath, like CI).
build-server:
    go build -trimpath -o bin/mihomori ./cmd/mihomori

# Build the TUI client binary into bin/.
build-tui:
    go build -o bin/mihomori-tui ./cmd/mihomori-tui

# ── Quality ────────────────────────────────────────────────────────────────

# Run the full test suite (supervisor tests compile testdata/fake-mihomo).
test:
    go test ./...

# Static analysis — exactly what CI runs.
vet:
    go vet ./...

# Format all Go code.
fmt:
    go fmt ./...

# Tidy go.mod / go.sum.
tidy:
    go mod tidy

# CI parity: vet, trimpath server build, full tests.
check: vet build-server test

# ── Run from source ────────────────────────────────────────────────────────

# Run the server from source (env: CONTROL_PORT, DATA_DIR, MIXED_PORT, ...).
# Extra args are passed through to the program, e.g. `just run --version`.
run *args:
    go run ./cmd/mihomori {{args}}

# Run the TUI client against a server (default http://127.0.0.1:8080).
# Extra args are passed through, e.g. `just run-tui -endpoint http://host:8080`.
run-tui *args:
    go run ./cmd/mihomori-tui {{args}}

# ── Frontend assets ────────────────────────────────────────────────────────

# Download the metacubexd dashboard into internal/server/static/web/ for go:embed.
ui:
    bash scripts/fetch-ui.sh

# ── Docker ─────────────────────────────────────────────────────────────────

# Build the local all-in-one image (UI/kernel versions from Dockerfile ARGs).
image:
    docker build -t mihomori:dev .

# Build + start the dev stack (from source, ./data-dev, host networking).
dev-up:
    docker compose -f docker-compose.development.yml up -d --build

# Stop the dev stack.
dev-down:
    docker compose -f docker-compose.development.yml down

# Follow dev stack logs.
dev-logs:
    docker compose -f docker-compose.development.yml logs -f

# Start the production compose stack (pre-built image).
up:
    docker compose up -d

# Stop the production compose stack.
down:
    docker compose down

# ── Nix ────────────────────────────────────────────────────────────────────

# Build the Nix package.
nix-build:
    nix build .#mihomori

# Run the flake checks (NixOS module evaluation).
nix-check:
    nix flake check

# ── Release ────────────────────────────────────────────────────────────────

# Local goreleaser dry run (builds archives, publishes nothing).
release-snapshot:
    goreleaser release --snapshot --clean
