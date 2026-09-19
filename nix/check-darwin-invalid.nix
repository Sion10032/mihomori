# nix-darwin configuration with no mihomo kernel configured, used by
# `nix flake check` to verify the module reports the missing kernel instead of
# failing obscurely. `mihomo = null` (with `mihomoBin` left at its null
# default) is the reachable form of "no kernel" on aarch64-darwin, where
# `pkgs.mihomo` is available.
{ ... }:
{
  system.primaryUser = "mihomori-test";
  system.stateVersion = 6;
  documentation.enable = false;

  services.mihomori = {
    enable = true;
    mihomo = null;
  };
}
