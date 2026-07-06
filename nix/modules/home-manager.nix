self: { config, lib, pkgs, ... }:

let
  cfg = config.services.usagent;
  format = pkgs.formats.yaml {};
  defaultPackage = self.packages.${pkgs.stdenv.hostPlatform.system}.usagent;
  generatedConfig = format.generate "usagent.yaml" (lib.recursiveUpdate cfg.config {
    server = (cfg.config.server or {}) // {
      host = cfg.host;
      port = cfg.port;
      statePath = cfg.statePath;
    };
  });
in
{
  options.services.usagent = {
    enable = lib.mkEnableOption "usagent usage/quota microservice user service";

    package = lib.mkOption {
      type = lib.types.package;
      default = defaultPackage;
      defaultText = lib.literalExpression "inputs.usagent.packages.${pkgs.stdenv.hostPlatform.system}.usagent";
      description = "usagent package to run.";
    };

    host = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1";
      description = "Address for usagent to listen on.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 8787;
      description = "Port for usagent to listen on.";
    };

    statePath = lib.mkOption {
      type = lib.types.str;
      default = "${config.xdg.stateHome}/usagent/snapshot.json";
      description = "Runtime snapshot path under the user's state directory by default.";
    };

    config = lib.mkOption {
      type = format.type;
      default = {
        server.readAuth.mode = "none";
        providers.claudeOAuth = {
          enabled = false;
          credentialsPath = "${config.home.homeDirectory}/.claude/.credentials.json";
          endpointUrl = "https://api.anthropic.com/api/oauth/usage";
          betaHeader = "oauth-2025-04-20";
        };
        providers.openai = {
          enabled = false;
          apiKeyEnv = "OPENAI_ADMIN_KEY";
          baseUrl = "https://api.openai.com";
          budgets = [];
        };
        providers.zAi = {
          enabled = false;
          endpointUrl = "https://api.z.ai/api/monitor/usage/quota/limit";
          tokenEnv = "ZAI_API_KEY";
          tokenEnvFallbacks = [ "GLM_API_KEY" ];
          authScheme = "bearer";
          authHeader = "Authorization";
          excludeLimitTypes = [ "TIME_LIMIT" ];
        };
        providers.custom = [];
        usageView.providers = [ "claude-code" "openai" "z-ai" ];
        quota.refreshMs = 300000;
      };
      description = ''
        YAML configuration rendered for usagent. Do not put plaintext tokens here:
        Nix store-generated config is world-readable. Prefer runtime credential
        files, token env var names (for example OPENAI_ADMIN_KEY/ZAI_API_KEY),
        or an EnvironmentFile outside the Nix store for secrets.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Optional user runtime environment file; keep it outside the Nix store if it contains secrets.";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    systemd.user.services.usagent = {
      Unit = {
        Description = "usagent usage/quota microservice";
        After = [ "network-online.target" ];
      };
      Service = {
        ExecStart = "${lib.getExe cfg.package} --config ${generatedConfig}";
        Restart = "on-failure";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
      };
      Install.WantedBy = [ "default.target" ];
    };
  };
}
