# NixOS module for mihomori (services.mihomori.*).
#
# Import into your NixOS configuration:
#
#   inputs.mihomori.url = "github:Sion10032/mihomori";
#   # in modules:
#   inputs.mihomori.nixosModules.default
#
# The systemd unit is based on deploy/mihomori-ctl.sh's embedded unit;
# capabilities follow the upstream nixpkgs mihomo module's scheme
# (opt-in tunMode / processesInfo).
{ self }:
{
  pkgs,
  config,
  lib,
  ...
}:

let
  common = import ./common.nix { inherit self lib pkgs; };
  cfg = config.services.mihomori;
  stateDirMatch = builtins.match "/var/lib/([^/]+)" cfg.dataDir;

  # Same capability scheme as the upstream nixpkgs mihomo module: no
  # capabilities by default, TUN support and process-info rules opt in.
  # The supervised mihomo child inherits them.
  AmbientCapabilities =
    lib.optional cfg.tunMode "CAP_NET_ADMIN"
    ++ lib.optionals cfg.processesInfo [
      "CAP_DAC_READ_SEARCH"
      "CAP_SYS_PTRACE"
    ];
  CapabilityBoundingSet = AmbientCapabilities;
in
{
  options.services.mihomori = common.options { inherit lib pkgs config; } // {
    user = lib.mkOption {
      type = lib.types.str;
      default = "mihomori";
      description = "User the service runs as.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "mihomori";
      description = "Group the service runs as.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Open the firewall for `controlPort` (and `mixedPort` when non-zero).
        The internal Clash API port is never exposed.
      '';
    };

    tunMode = lib.mkEnableOption ''
      necessary capabilities for the service (and the mihomo kernel it
      supervises) for TUN mode to function properly.

      Keep in mind that you still need to enable TUN mode manually in the
      dashboard or mihomo configuration.
    '';

    processesInfo = lib.mkEnableOption ''
      necessary capabilities for mihomo rules about process information
      such as `process-name`
    '';
  };

  config = lib.mkIf cfg.enable {
    assertions = common.assertions cfg;

    users.users = lib.mkIf (cfg.user == "mihomori") {
      mihomori = {
        isSystemUser = true;
        group = cfg.group;
        description = "mihomori daemon user";
        home = cfg.dataDir;
        createHome = false;
      };
    };

    users.groups = lib.mkIf (cfg.group == "mihomori") {
      mihomori = { };
    };

    environment.systemPackages = [ cfg.package ];

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedTCPPorts = [ cfg.controlPort ] ++ lib.optional (cfg.mixedPort != 0) cfg.mixedPort;
    };

    systemd.services.mihomori = {
      description = "mihomori (All-in-One dashboard + mihomo supervisor)";
      documentation = [ "https://github.com/Sion10032/mihomori" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      environment = common.env cfg;

      serviceConfig = {
        Type = "simple";
        User = cfg.user;
        Group = cfg.group;
        ExecStart = lib.getExe cfg.package;
        # "+" runs this step with full privileges so a custom dataDir exists
        # before the sandboxed service starts.
        ExecStartPre = "+${lib.getExe' pkgs.coreutils "mkdir"} -p ${cfg.dataDir}";
        WorkingDirectory = cfg.dataDir;
        TimeoutStopSec = 15;
        KillSignal = "SIGTERM";
        Restart = "on-failure";
        RestartSec = 5;
        inherit AmbientCapabilities CapabilityBoundingSet;
        NoNewPrivileges = true;
        StateDirectory = lib.optionals (stateDirMatch != null) stateDirMatch;
        ProtectSystem = "strict";
        ReadWritePaths = [ cfg.dataDir ];
        PrivateTmp = true;
      };
    };
  };
}
