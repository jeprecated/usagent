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
        usagent = pkgs.stdenvNoCC.mkDerivation {
          pname = "usagent";
          version = "0.1.0";
          src = ./.;
          dontBuild = true;
          installPhase = ''
            runHook preInstall
            mkdir -p $out/lib/usagent $out/bin
            cp -R package.json src config.example.yaml $out/lib/usagent/
            makeWrapper ${pkgs.nodejs_24}/bin/node $out/bin/usagent \
              --add-flags $out/lib/usagent/src/bin/usagent.mjs
            makeWrapper ${pkgs.nodejs_24}/bin/node $out/bin/usagent-claude-statusline \
              --add-flags $out/lib/usagent/src/bin/usagent-claude-statusline.mjs
            runHook postInstall
          '';
          nativeBuildInputs = [ pkgs.makeWrapper ];
          meta = {
            description = "Agent usage/quota microservice";
            mainProgram = "usagent";
          };
        };
      in {
        packages.default = usagent;
        packages.usagent = usagent;
        apps.default = flake-utils.lib.mkApp { drv = usagent; };
        devShells.default = pkgs.mkShell {
          packages = [ pkgs.nodejs_24 pkgs.jujutsu pkgs.devenv pkgs.jq pkgs.curl ];
        };
      });
}
