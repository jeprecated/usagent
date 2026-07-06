{
  description = "usagent agent usage/quota microservice";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        version = "0.1.0";
        usagent = pkgs.buildGoModule {
          pname = "usagent";
          inherit version;
          src = ./.;
          vendorHash = "sha256-g+yaVIx4jxpAQ/+WrGKxhVeliYx7nLQe/zsGpxV4Fn4=";
          subPackages = [ "cmd/usagent" ];
          ldflags = [ "-s" "-w" ];
          meta = {
            description = "Agent usage/quota microservice";
            mainProgram = "usagent";
          };
        };
      in {
        packages.default = usagent;
        packages.usagent = usagent;
        packages.oci = pkgs.dockerTools.buildLayeredImage {
          name = "usagent";
          tag = version;
          contents = [ pkgs.cacert ];
          config = {
            User = "10001:10001";
            ExposedPorts = { "8787/tcp" = {}; };
            Env = [
              "USAGENT_CONFIG=/etc/usagent/config.yaml"
              "USAGENT_HOST=0.0.0.0"
              "USAGENT_PORT=8787"
            ];
            Entrypoint = [ "${usagent}/bin/usagent" ];
            Labels = { "org.opencontainers.image.title" = "usagent"; };
          };
        };
        apps.default = flake-utils.lib.mkApp { drv = usagent; };
        apps.usagent = flake-utils.lib.mkApp { drv = usagent; };
        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.jujutsu pkgs.devenv pkgs.jq pkgs.curl pkgs.docker-client ];
        };
      }) // {
        nixosModules.usagent = import ./nix/modules/nixos.nix self;
        homeManagerModules.usagent = import ./nix/modules/home-manager.nix self;
      };
}
