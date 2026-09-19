# nix-darwin module for mihomori (services.mihomori.*).
#
# Import into your nix-darwin configuration:
#
#   inputs.mihomori.url = "github:Sion10032/mihomori";
#   # in modules:
#   inputs.mihomori.darwinModules.default
#
# The service runs as `system.primaryUser` through a per-user launchd agent
# (~/Library/LaunchAgents). User-level agents cannot create utun devices, so
# TUN mode is not available — use the NixOS module on Linux for that.
#
# Do not enable this together with the home-manager module on the same machine:
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
in
{
  options.services.mihomori = common.options {
    inherit lib pkgs config;
    dataDirDefault = "${config.system.primaryUserHome}/Library/Application Support/mihomori";
  };

  config = lib.mkIf cfg.enable {
    assertions = common.assertions cfg;

    environment.systemPackages = [ cfg.package ];

    # nix-darwin only runs the hook names hardcoded in
    # modules/system/activation-scripts.nix, so a custom activationScripts entry
    # would never execute; `extraActivation` is its documented user hook and is
    # interpolated before the launchd hooks load the agent.
    system.activationScripts.extraActivation.text = lib.mkAfter ''
      mkdir -p ${lib.escapeShellArg cfg.dataDir}
      chown ${config.system.primaryUser} ${lib.escapeShellArg cfg.dataDir}
    '';

    launchd.user.agents.mihomori = {
      serviceConfig = {
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
