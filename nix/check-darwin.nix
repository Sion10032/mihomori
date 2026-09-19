# Minimal nix-darwin configuration used by `nix flake check` to evaluate the
# nix-darwin module.
{ ... }:
{
  system.primaryUser = "mihomori-test";
  system.stateVersion = 6;
  documentation.enable = false;

  services.mihomori = {
    enable = true;
    controlToken = "test-token";
    clashSecret = "test-secret";
  };
}
