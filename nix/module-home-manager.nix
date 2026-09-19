# Home Manager module for mihomori (services.mihomori.*).
#
# Import into your Home Manager configuration:
#
#   inputs.mihomori.url = "github:Sion10032/mihomori";
#   # in modules:
#   inputs.mihomori.homeManagerModules.default
#
# The service runs as the logged-in user: Linux uses systemd --user, Darwin
# uses a launchd agent. User-level services cannot acquire CAP_NET_ADMIN, so
# TUN mode is not available here — use the NixOS module for that.
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
in
{
  options.services.mihomori = common.options {
    inherit lib pkgs config;
    dataDirDefault = "${config.home.homeDirectory}/.local/state/mihomori";
  };

  config = lib.mkIf cfg.enable {
    assertions = common.assertions cfg;

    home.packages = [ cfg.package ];

    home.activation.mihomoriDataDir = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
      run mkdir -p ${lib.escapeShellArg cfg.dataDir}
    '';

    systemd.user.services.mihomori = {
      Unit = {
        Description = "mihomori (All-in-One dashboard + mihomo supervisor)";
        Documentation = [ "https://github.com/Sion10032/mihomori" ];
        After = [ "network-online.target" ];
        Wants = [ "network-online.target" ];
      };
      Service = {
        ExecStart = lib.getExe cfg.package;
        Environment = lib.mapAttrsToList (name: value: "${name}=${value}") (common.env cfg);
        Restart = "on-failure";
        RestartSec = 5;
      };
      Install.WantedBy = [ "default.target" ];
    };
  };
}
