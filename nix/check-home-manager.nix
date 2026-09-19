# Minimal Home Manager configuration used by `nix flake check` to evaluate the
# home-manager module. The Darwin evaluation overrides `home.homeDirectory`
# with `lib.mkForce` (see flake.nix).
{ ... }:
{
  home.stateVersion = "26.05";
  home.username = "mihomori-test";
  home.homeDirectory = "/home/mihomori-test";
  home.enableNixpkgsReleaseCheck = false;

  services.mihomori = {
    enable = true;
    controlToken = "test-token";
    clashSecret = "test-secret";
  };
}
