# Minimal NixOS configuration used by `nix flake check` to eval the module.
{ ... }:
{
  services.mihomori = {
    enable = true;
    tunMode = true;
    controlToken = "test-token";
    clashSecret = "test-secret";
  };
}
