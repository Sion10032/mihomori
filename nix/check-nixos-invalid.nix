# Invalid NixOS configuration (mihomo + mihomoBin both set) used by
# `nix flake check` to verify the mutual-exclusion assertion fires.
{ ... }:
{
  services.mihomori = {
    enable = true;
    mihomoBin = "/opt/mihomo";
  };
}
