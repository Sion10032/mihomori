# Parameterized buildGoModule for mihomori.
#
# webuiEnable = true  → fetch the dashboard release tarball (versions.env
#                       METACUBEXD_VERSION) and unpack it into
#                       internal/server/static/web/ so go:embed bundles it.
# webuiEnable = false → keep the stub error page (same as a bare checkout).
{
  pkgs,
  lib,
  webuiEnable ? true,
  version ? "0.1.0",
  commit ? "none",
  # GOPROXY used while fetching Go modules (fixed-output derivation, so this
  # never affects reproducibility). Override when the default proxy is
  # unreachable, e.g. "https://goproxy.cn,direct".
  goProxy ? "https://proxy.golang.org,direct",
}:

let
  # Keep in sync with versions.env (METACUBEXD_VERSION).
  uiVersion = "v1.271.0";
  uiDist = pkgs.fetchurl {
    url = "https://github.com/MetaCubeX/metacubexd/releases/download/${uiVersion}/compressed-dist.tgz";
    hash = "sha256-a5d5A9tAONJzdZI0KrVQIxh4/UEPo9Q2+tKHZfTQr8c=";
  };
in
pkgs.buildGoModule {
  pname = "mihomori";
  inherit version;
  src = ../.;
  vendorHash = "sha256-lShMe3TOu9EIsp1/UY27zFsEqSQNwVE8zrhsa8A2j0U=";
  # nixpkgs declares GOPROXY as an impureEnvVar on the go-modules fetch
  # derivation: the daemon environment (usually empty, i.e. "") overrides
  # any value set via `env` and Go then falls back to proxy.golang.org. This
  # preBuild hook runs after that injection and restores a usable proxy —
  # an explicitly injected non-empty GOPROXY still wins. It also applies to
  # the go-modules fetch phase; the main build re-exports GOPROXY=off
  # afterwards, so this only affects module fetching.
  preBuild = ''
    export GOPROXY="''${GOPROXY:-${lib.escapeShellArg goProxy}}"
  '';
  subPackages = [ "cmd/mihomori" ];
  buildFlags = [ "-trimpath" ];
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
    "-X main.commit=${commit}"
    "-X main.date=unknown"
  ];
  postUnpack = lib.optionalString webuiEnable ''
    mkdir -p $sourceRoot/internal/server/static/web
    tar -xzf ${uiDist} -C $sourceRoot/internal/server/static/web
  '';
  meta = with lib; {
    description = "ClashMeta Dashboard backend server";
    homepage = "https://github.com/Sion10032/mihomori";
    license = licenses.mit;
    platforms = platforms.linux ++ platforms.darwin;
    mainProgram = "mihomori";
  };
}
