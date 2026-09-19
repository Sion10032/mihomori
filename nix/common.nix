# Shared option definitions and env mapping for the mihomori modules
# (services.mihomori.*).
#
# Import as: common = import ./common.nix { inherit self lib pkgs; }
#
# Exposes:
#   options          — function { lib, pkgs, config, dataDirDefault } → attrset of
#                      option declarations for services.mihomori
#   env              — cfg → attrs of environment variables understood by the
#                      server (internal/server/config/config.go)
#   assertions       — cfg → module-system-style assertion attrsets
{
  self,
  lib,
  pkgs,
}:

let
  # Some platforms have no pkgs.mihomo build; fall back to null where it is
  # unavailable so the assertion below can point at mihomoBin instead of an
  # eval-time crash.
  mihomoDefault =
    if lib.meta.availableOn pkgs.stdenv.hostPlatform pkgs.mihomo then pkgs.mihomo else null;

  assertionList =
    cfg:
    [
      {
        assertion = cfg.mihomo == null || cfg.mihomoBin == null;
        message = "services.mihomori.mihomo and services.mihomori.mihomoBin are mutually exclusive.";
      }
      {
        assertion = cfg.mihomo != null || cfg.mihomoBin != null;
        message = ''
          services.mihomori has no mihomo kernel configured${
            lib.optionalString (
              mihomoDefault == null
            ) ": pkgs.mihomo is not available on ${pkgs.stdenv.hostPlatform.system}"
          }. Set services.mihomori.mihomoBin to an externally managed mihomo
          binary, or override services.mihomori.mihomo.
        '';
      }
    ];
in
{
  options = { lib, pkgs, config, dataDirDefault ? "/var/lib/mihomori" }: with lib; {
    enable = mkOption {
      type = types.bool;
      default = false;
      description = ''
        Whether to enable the mihomori service (dashboard UI +
        mihomo supervisor).
      '';
    };

    package = mkOption {
      type = types.package;
      default =
        if config.services.mihomori.webui.enable
        then self.packages.${pkgs.system}.mihomori
        else self.packages.${pkgs.system}.mihomori-minimal;
      defaultText = literalMD
        "`self.packages.\${system}.mihomori` (`…-minimal` when `webui.enable = false`)";
      description = ''
        The mihomori package to use. Defaults to the build with
        the dashboard UI embedded; set
        `services.mihomori.webui.enable = false` to get the stub
        build instead.
      '';
    };

    webui.enable = mkOption {
      type = types.bool;
      default = true;
      description = ''
        Whether the default `package` bundles the metacubexd dashboard UI.
        Ignored when `package` is set explicitly. The UI can also be provided
        at runtime via `uiDist`.
      '';
    };

    uiDist = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = ''
        Serve the dashboard from this on-disk directory instead of the
        embedded copy (UI_DIST). null = use whatever the binary embeds.
      '';
    };

    controlPort = mkOption {
      type = types.port;
      default = 8080;
      description = "Dashboard UI + control/Clash API listen port (CONTROL_PORT).";
    };

    clashApiPort = mkOption {
      type = types.port;
      default = 9090;
      description = ''
        mihomo's internal Clash API port (CLASH_API_PORT). Only reachable
        inside the service; browsers talk to `controlPort`.
      '';
    };

    mixedPort = mkOption {
      type = types.ints.unsigned;
      default = 0;
      description = ''
        Lock mihomo's mixed proxy port (MIXED_PORT). 0 (default) lets the
        dashboard edit and persist it.
      '';
    };

    dataDir = mkOption {
      type = types.str;
      default = dataDirDefault;
      description = ''
        Directory for profiles/, active.yaml, geo caches (DATA_DIR).
        `/var/lib/<name>` paths are managed via StateDirectory.
      '';
    };

    mihomo = mkOption {
      type = types.nullOr types.package;
      default = mihomoDefault;
      defaultText = literalMD "`pkgs.mihomo`, or `null` where it is unavailable";
      description = ''
        mihomo kernel package supervised by the server (MIHOMO_BIN).
        Set to null and use `mihomoBin` to point at an external binary.
      '';
    };

    mihomoBin = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = ''
        Absolute path to an externally managed mihomo binary (MIHOMO_BIN).
        Mutually exclusive with `mihomo`.
      '';
    };

    controlToken = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = ''
        Bearer token gating /api/control/** (CONTROL_TOKEN). null leaves it
        unset — set a value for any non-local deployment.
      '';
    };

    clashSecret = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = "mihomo external-controller secret (CLASH_SECRET). null = unset.";
    };

    githubToken = mkOption {
      type = types.nullOr types.str;
      default = null;
      description = ''
        Optional GitHub token for authenticated rate limits when downloading
        mihomo/UI assets (GITHUB_TOKEN). null = unset.
      '';
    };
  };

  env =
    cfg:
    {
      CONTROL_PORT = toString cfg.controlPort;
      CLASH_API_PORT = toString cfg.clashApiPort;
      DATA_DIR = cfg.dataDir;
    }
    // lib.optionalAttrs (cfg.mixedPort != 0) { MIXED_PORT = toString cfg.mixedPort; }
    // lib.optionalAttrs (cfg.mihomo != null) { MIHOMO_BIN = "${cfg.mihomo}/bin/mihomo"; }
    // lib.optionalAttrs (cfg.mihomoBin != null) { MIHOMO_BIN = cfg.mihomoBin; }
    // lib.optionalAttrs (cfg.uiDist != null) { UI_DIST = cfg.uiDist; }
    // lib.optionalAttrs (cfg.controlToken != null) { CONTROL_TOKEN = cfg.controlToken; }
    // lib.optionalAttrs (cfg.clashSecret != null) { CLASH_SECRET = cfg.clashSecret; }
    // lib.optionalAttrs (cfg.githubToken != null) { GITHUB_TOKEN = cfg.githubToken; };

  assertions = assertionList;
}
