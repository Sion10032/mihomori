{
  description = "mihomori";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "x86_64-darwin"
      "aarch64-darwin"
    ];
    forAllSystems = nixpkgs.lib.genAttrs systems;
  in {
    packages = forAllSystems (
      system: let
        pkgs = import nixpkgs { inherit system; };
        version = "0.1.0";
        commit = self.rev or "none";
      in {
        mihomori = pkgs.callPackage ./nix/server.nix {
          inherit version commit;
          webuiEnable = true;
        };
        mihomori-minimal = pkgs.callPackage ./nix/server.nix {
          inherit version commit;
          webuiEnable = false;
        };
        default = self.packages.${system}.mihomori;
      }
    );

    devShells = forAllSystems (
      system: let
        pkgs = import nixpkgs { inherit system; };
      in {
        default = pkgs.mkShellNoCC {
          packages = with pkgs; [
            go
            gopls
            gotools
          ];
        };
      }
    );

    checks = forAllSystems (
      system: let
        pkgs = import nixpkgs { inherit system; };
        lib = nixpkgs.lib;

        nixosConfig =
          modules:
          (lib.nixosSystem {
            modules = [ { nixpkgs.hostPlatform = system; } self.nixosModules.default ] ++ modules;
          }).config;

        # Restrict assertion messages to ones coming from our module so a
        # bare eval's unrelated assertions (if any) don't false-fail.
        mihomoriAssertionMessages =
          config:
          builtins.filter (lib.hasInfix "mihomori") (
            map (a: a.message) (builtins.filter (a: !a.assertion) config.assertions)
          );
      in {
        nixos-module-eval =
          let
            valid = nixosConfig [ ./nix/check-nixos.nix ];
            unit = valid.systemd.units."mihomori.service".text;
            failedValid = mihomoriAssertionMessages valid;

            invalid = nixosConfig [ ./nix/check-nixos-invalid.nix ];
            failedInvalid = mihomoriAssertionMessages invalid;

            # No tunMode/processesInfo → no capabilities granted.
            unitNoCaps = (nixosConfig [ { services.mihomori.enable = true; } ])
              .systemd.units."mihomori.service".text;
          in
          if failedValid != [ ] then
            throw "valid module check config has failed assertions: ${toString failedValid}"
          else if failedInvalid == [ ] then
            throw "mihomo/mihomoBin mutual exclusion was not rejected"
          else
            pkgs.runCommand "mihomori-nixos-module-eval" {
              inherit unit unitNoCaps;
            } ''
              grep -q "ExecStart=.*bin/mihomori" <<< "$unit" \
                || { echo "ExecStart missing from generated unit:"; echo "$unit"; exit 1; }
              grep -q "CONTROL_TOKEN=test-token" <<< "$unit" \
                || { echo "CONTROL_TOKEN missing from generated unit"; exit 1; }
              grep -q "DATA_DIR=/var/lib/mihomori" <<< "$unit" \
                || { echo "DATA_DIR missing from generated unit"; exit 1; }
              grep -q "AmbientCapabilities=CAP_NET_ADMIN" <<< "$unit" \
                || { echo "tunMode did not grant CAP_NET_ADMIN"; exit 1; }
              if grep -q "CAP_NET_ADMIN" <<< "$unitNoCaps"; then
                echo "CAP_NET_ADMIN granted without tunMode"; exit 1
              fi
              touch "$out"
            '';
      }
    );

    nixosModules.default = import ./nix/module-nixos.nix { inherit self; };
  };
}
