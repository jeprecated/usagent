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
    enable = lib.mkEnableOption "usagent usage/quota microservice";

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

    stateDir = lib.mkOption {
      type = lib.types.str;
      default = "usagent";
      description = "systemd StateDirectory name for persisted snapshots.";
    };

    statePath = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/${cfg.stateDir}/snapshot.json";
      description = "Runtime snapshot path. This path should be writable by the service.";
    };

    config = lib.mkOption {
      type = format.type;
      default = {
        server.readAuth.mode = "none";
        providers.claudeOAuth = {
          enabled = false;
          credentialsPath = "/run/credentials/usagent/claude-credentials.json";
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
        providers.cursor = {
          enabled = false;
          authPath = "/run/credentials/usagent/cursor-auth.json";
          endpointUrl = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage";
          tokenEnv = "CURSOR_ACCESS_TOKEN";
        };
        providers.custom = [];
        usageView.providers = [ "claude-code" "openai" "z-ai" "cursor" ];
        quota.refreshMs = 300000;
      };
      description = ''
        YAML configuration rendered for usagent. Do not put plaintext tokens here:
        Nix store-generated config is world-readable. Use runtime credential paths,
        token env var names (for example OPENAI_ADMIN_KEY/ZAI_API_KEY), systemd
        credentials, agenix, sops-nix, or environment files for secrets.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Optional runtime environment file; keep it outside the Nix store if it contains secrets.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.usagent = {
      description = "usagent usage/quota microservice";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} serve --config ${generatedConfig}";
        Restart = "on-failure";
        DynamicUser = true;
        StateDirectory = cfg.stateDir;
        WorkingDirectory = "/var/lib/${cfg.stateDir}";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectHome = "read-only";
        ProtectSystem = "strict";
        ReadWritePaths = [ "/var/lib/${cfg.stateDir}" ];
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
      };
    };
  };
}
