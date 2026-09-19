# Home Manager module for mihomori (services.mihomori.*).
#
# Import into your Home Manager configuration:
#
#   inputs.mihomori.url = "github:Sion10032/mihomori";
#   # in modules:
#   inputs.mihomori.homeManagerModules.default
#
# The service runs as the logged-in user: Linux uses systemd --user, Darwin
# uses a launchd agent in ~/Library/LaunchAgents. User-level services cannot
# acquire CAP_NET_ADMIN, so TUN mode is not available here — use the NixOS
# module for that.
#
# Do not enable this together with the nix-darwin module on the same machine:
# both install a launchd agent.
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
  isDarwin = pkgs.stdenv.hostPlatform.isDarwin;

  dataDirDefault =
    if isDarwin then
      "${config.home.homeDirectory}/Library/Application Support/mihomori"
    else
      "${config.home.homeDirectory}/.local/state/mihomori";
in
{
  options.services.mihomori = common.options {
    inherit lib pkgs config dataDirDefault;
  };

  config = lib.mkIf cfg.enable {
    assertions = common.assertions cfg;

    home.packages = [ cfg.package ];

    home.activation.mihomoriDataDir = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
      run mkdir -p ${lib.escapeShellArg cfg.dataDir}
    '';

    systemd.user.services.mihomori = lib.mkIf (!isDarwin) {
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

    launchd.agents.mihomori = lib.mkIf isDarwin {
      enable = true;
      domain = "gui";
      config = {
        Label = "org.mihomori.mihomori";
        ProgramArguments = [ (lib.getExe cfg.package) ];
        EnvironmentVariables = common.env cfg;
        WorkingDirectory = cfg.dataDir;
        RunAtLoad = true;
        KeepAlive = { SuccessfulExit = false; };
        StandardOutPath = "${cfg.dataDir}/mihomori.log";
        StandardErrorPath = "${cfg.dataDir}/mihomori.err.log";
      };
    };
  };
}
