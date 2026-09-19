{
  description = "mihomori";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    # dev-only inputs: home-manager / nix-darwin live in dev/private so they
    # never enter this flake's lock file. Nix is lazy, so the private flake is
    # only fetched when `checks` is evaluated.
    loadPrivateFlake =
      path:
      let
        flakeHash = nixpkgs.lib.removeSuffix "\n" (builtins.readFile "${toString path}.narHash");
        flakePath = "path:${toString path}?narHash=${flakeHash}";
      in
      builtins.getFlake (builtins.unsafeDiscardStringContext flakePath);

    privateInputs = (loadPrivateFlake ./dev/private).inputs;

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

        privateInputsProblems =
          lib.optional (!(privateInputs ? home-manager)) "dev/private must declare a home-manager input"
          ++ lib.optional (
            (privateInputs ? home-manager) && !((privateInputs.home-manager.lib or { }) ? homeManagerConfiguration)
          ) "home-manager input has no lib.homeManagerConfiguration"
          ++ lib.optional (!(privateInputs ? nix-darwin)) "dev/private must declare a nix-darwin input"
          ++ lib.optional (
            (privateInputs ? nix-darwin) && !((privateInputs.nix-darwin.lib or { }) ? darwinSystem)
          ) "nix-darwin input has no lib.darwinSystem";
      in
      {
        private-inputs-available =
          if privateInputsProblems != [ ] then
            throw "dev/private is missing required inputs: ${lib.concatStringsSep "; " privateInputsProblems}"
          else
            pkgs.runCommand "mihomori-private-inputs-available" { } ''
              touch "$out"
            '';

        home-manager-module-eval =
          let
            hmLinux = (privateInputs.home-manager.lib.homeManagerConfiguration {
              pkgs = import nixpkgs { system = "x86_64-linux"; };
              modules = [
                self.homeManagerModules.default
                ./nix/check-home-manager.nix
              ];
            }).config;

            hmInvalid = (privateInputs.home-manager.lib.homeManagerConfiguration {
              pkgs = import nixpkgs { system = "x86_64-linux"; };
              modules = [
                self.homeManagerModules.default
                ./nix/check-home-manager-invalid.nix
              ];
            }).config;

            hmDarwin = (privateInputs.home-manager.lib.homeManagerConfiguration {
              pkgs = import nixpkgs { system = "aarch64-darwin"; };
              modules = [
                self.homeManagerModules.default
                ./nix/check-home-manager.nix
                { home.homeDirectory = lib.mkForce "/Users/mihomori-test"; }
              ];
            }).config;

            agent = hmDarwin.launchd.agents.mihomori;
            darwinDataDir = "/Users/mihomori-test/Library/Application Support/mihomori";

            unit = hmLinux.systemd.user.services.mihomori;
            service = hmLinux.services.mihomori;

            hmInvalidRejected =
              !(builtins.tryEval (builtins.deepSeq (mihomoriAssertionMessages hmInvalid) true)).success;

            problems =
              lib.optional (!(unit.Install.WantedBy == [ "default.target" ]))
                "systemd unit is not wanted by default.target"
              ++ lib.optional (!(lib.elem "CONTROL_TOKEN=test-token" unit.Service.Environment))
                "CONTROL_TOKEN missing from the generated systemd unit"
              ++ lib.optional (service.dataDir != "/home/mihomori-test/.local/state/mihomori")
                "unexpected home-manager dataDir default: ${service.dataDir}"
              ++ lib.optional (hmLinux.services.mihomori ? tunMode)
                "user-level module must not expose tunMode"
              ++ lib.optional (!agent.enable) "launchd agent is not enabled"
              ++ lib.optional (agent.domain != "gui") "launchd agent domain should be gui"
              ++ lib.optional (agent.config.Label != "org.mihomori.mihomori")
                "unexpected launchd Label: ${agent.config.Label}"
              ++ lib.optional (agent.config.EnvironmentVariables.CONTROL_TOKEN != "test-token")
                "CONTROL_TOKEN missing from the launchd EnvironmentVariables"
              ++ lib.optional (agent.config.EnvironmentVariables.DATA_DIR != darwinDataDir)
                "unexpected darwin DATA_DIR: ${agent.config.EnvironmentVariables.DATA_DIR}"
              ++ lib.optional (agent.config.RunAtLoad != true) "RunAtLoad is not set"
              ++ lib.optional (agent.config.KeepAlive.SuccessfulExit != false)
                "KeepAlive.SuccessfulExit should be false"
              ++ lib.optional (agent.config.WorkingDirectory != darwinDataDir)
                "launchd WorkingDirectory should default to dataDir"
              ++ lib.optional (hmDarwin.systemd.user.services ? mihomori)
                "darwin must not generate a systemd unit"
              ++ lib.optional (hmDarwin.services.mihomori.dataDir != darwinDataDir)
                "unexpected home-manager darwin dataDir default: ${hmDarwin.services.mihomori.dataDir}"
              ++ lib.optional (!(hmLinux.home.activation ? mihomoriDataDir))
                "home.activation.mihomoriDataDir is missing (dataDir would not be created)"
              ++ mihomoriAssertionMessages hmLinux
              # home-manager's `config` throws outright once an assertion fails
              # (unlike NixOS, whose `config.assertions` stays readable), so a
              # rejected config is only observable as a failed evaluation.
              ++ lib.optional (!hmInvalidRejected)
                "mihomo/mihomoBin mutual exclusion was not rejected by the home-manager module";
          in
          if problems != [ ] then
            throw "home-manager module check failed: ${lib.concatStringsSep "; " problems}"
          else
            pkgs.runCommand "mihomori-home-manager-module-eval" { } ''
              touch "$out"
            '';

        darwin-module-eval =
          let
            evalDarwin =
              hostPlatform: modules:
              (privateInputs.nix-darwin.lib.darwinSystem {
                modules = [
                  self.darwinModules.default
                ] ++ modules ++ [ { nixpkgs.hostPlatform = hostPlatform; } ];
              }).config;

            valid = evalDarwin "aarch64-darwin" [ ./nix/check-darwin.nix ];
            invalid = evalDarwin "aarch64-darwin" [ ./nix/check-darwin-invalid.nix ];

            agent = valid.launchd.user.agents.mihomori.serviceConfig;
            darwinDataDir = "/Users/mihomori-test/Library/Application Support/mihomori";

            # The assembled activate script, truncated at the first mention of our
            # plist — i.e. everything that runs before `launchctl load -w
            # …/org.mihomori.mihomori.plist`. `extraActivation` must land in here.
            beforeAgentLoad =
              builtins.head (
                lib.splitString "org.mihomori.mihomori.plist" valid.system.activationScripts.script.text
              );

            problems =
              lib.optional (agent.Label != "org.mihomori.mihomori")
                "unexpected launchd Label: ${agent.Label}"
              ++ lib.optional (agent.EnvironmentVariables.CONTROL_TOKEN != "test-token")
                "CONTROL_TOKEN missing from the launchd job"
              ++ lib.optional (agent.EnvironmentVariables.DATA_DIR != darwinDataDir)
                "unexpected DATA_DIR: ${agent.EnvironmentVariables.DATA_DIR}"
              ++ lib.optional (agent.RunAtLoad != true) "RunAtLoad is not set"
              ++ lib.optional (agent.WorkingDirectory != darwinDataDir)
                "WorkingDirectory should default to dataDir"
              ++ lib.optional (valid.services.mihomori.dataDir != darwinDataDir)
                "unexpected nix-darwin dataDir default: ${valid.services.mihomori.dataDir}"
              ++ lib.optional (valid.services.mihomori ? tunMode)
                "user-level module must not expose tunMode"
              ++ lib.optional (
                builtins.length (lib.splitString "org.mihomori.mihomori.plist" valid.system.activationScripts.script.text) < 2
              ) "activation script never mentions the mihomori launchd plist"
              ++ lib.optional (
                !(lib.hasInfix "mkdir -p ${lib.escapeShellArg darwinDataDir}" beforeAgentLoad)
              ) "activation script does not create dataDir before the agent is loaded"
              ++ mihomoriAssertionMessages valid
              ++ lib.optional (mihomoriAssertionMessages invalid == [ ])
                "darwin must report that no mihomo kernel is configured";
          in
          if problems != [ ] then
            throw "nix-darwin module check failed: ${lib.concatStringsSep "; " problems}"
          else
            pkgs.runCommand "mihomori-darwin-module-eval" { } ''
              touch "$out"
            '';
      }
      // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
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
    homeManagerModules.default = import ./nix/module-home-manager.nix { inherit self; };
    homeManagerModule = self.homeManagerModules.default;
    homeModules = self.homeManagerModules;
    darwinModules.default = import ./nix/module-darwin.nix { inherit self; };

    apps = forAllSystems (
      system: let
        pkgs = import nixpkgs { inherit system; };
      in {
        update-dev-private-narHash = {
          type = "app";
          program = "${pkgs.writeShellScript "update-dev-private-narHash" ''
            set -euo pipefail
            if [ ! -f flake.nix ] || [ ! -d dev/private ]; then
              echo "run this from the repository root (dev/private must exist)" >&2
              exit 1
            fi
            nix --extra-experimental-features "nix-command flakes" flake lock ./dev/private
            tmp=$(mktemp)
            trap 'rm -f "$tmp"' EXIT
            nix --extra-experimental-features "nix-command flakes" hash path ./dev/private | tr -d '\n' > "$tmp"
            mv "$tmp" ./dev/private.narHash
            echo "dev/private.narHash updated: $(cat ./dev/private.narHash)"
          ''}";
        };
      }
    );
  };
}
