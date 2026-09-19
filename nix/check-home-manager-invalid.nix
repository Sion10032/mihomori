# Invalid Home Manager configuration (mihomo + mihomoBin both set) used by
# `nix flake check` to verify the mutual-exclusion assertion is wired into the
# home-manager module.
{ ... }:
{
  home.stateVersion = "26.05";
  home.username = "mihomori-test";
  home.homeDirectory = "/home/mihomori-test";
  home.enableNixpkgsReleaseCheck = false;

  services.mihomori = {
    enable = true;
    mihomoBin = "/opt/mihomo";
  };
}
