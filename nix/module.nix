# services.taproot: taproot as a hardened systemd service beside tailscaled.
#
# Nix owns config.yaml and writes it on every start, so the dashboard's edits
# to it last until the next restart. taproot.yaml is copied from
# taprootSettings only when the file does not exist: the dashboard owns it
# afterwards (it saves access, backup, and alert changes by renaming a new
# file over it), so later changes to taprootSettings do not reach a running
# install. Neither file is a symlink into the store.
{
  config,
  lib,
  pkgs,
  utils,
  ...
}:
let
  cfg = config.services.taproot;
  yaml = pkgs.formats.yaml { };
  configFile = yaml.generate "taproot-config.yaml" cfg.settings;
  taprootFile = yaml.generate "taproot.yaml" cfg.taprootSettings;

  # Outside a container every local process can connect from loopback, and
  # taproot treats loopback callers as admins.
  host = lib.toLower cfg.settings.server.host;
  hostIsLocalOrWildcard =
    lib.elem host [
      ""
      "*"
      "0.0.0.0"
      "::"
      "[::]"
      "::0"
      "localhost"
      "::1"
      "[::1]"
    ]
    || lib.hasPrefix "127." host
    || lib.hasPrefix "::ffff:127." host
    || lib.hasSuffix ".localhost" host;

  args = [
    "--config"
    "${cfg.dataDir}/config.yaml"
  ]
  ++ lib.optional cfg.localModel "--local-model"
  ++ cfg.extraArgs;
in
{
  options.services.taproot = {
    enable = lib.mkEnableOption "taproot, a tailnet-only AI proxy with quota tracking and a dashboard";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The taproot package. The flake's module sets it to the flake's build.";
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "taproot";
      description = "User the service runs as. The module creates the default user.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "taproot";
      description = "Group the service runs as. The module creates the default group.";
    };

    dataDir = lib.mkOption {
      type = lib.types.strMatching "/.+";
      default = "/var/lib/taproot";
      description = ''
        Directory for config.yaml, taproot.yaml, the OAuth credentials
        (auths/), and taproot's database, request logs, and backups. Back it
        up: it holds refresh tokens.
      '';
    };

    tailscale.address = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "100.64.0.1";
      description = ''
        This host's Tailscale address (`tailscale ip -4`). Taproot listens on
        it, and it is the default for `settings.server.host`.
      '';
    };

    settings = lib.mkOption {
      description = ''
        Upstream CLIProxyAPI settings, written to `config.yaml` on every
        start. config.example.yaml in the repository documents every key.
        The defaults match examples/taproot/config.yaml.
      '';
      default = { };
      type = lib.types.submodule {
        freeformType = yaml.type;
        options.server = lib.mkOption {
          default = { };
          description = "Listener settings.";
          type = lib.types.submodule {
            freeformType = yaml.type;
            options.host = lib.mkOption {
              type = lib.types.str;
              default = if cfg.tailscale.address == null then "" else cfg.tailscale.address;
              defaultText = lib.literalExpression ''if config.services.taproot.tailscale.address == null then "" else config.services.taproot.tailscale.address'';
              description = ''
                Address to listen on. It must not be a loopback or wildcard
                address: every local process can connect from loopback, and
                taproot treats loopback callers as admins.
              '';
            };
            options.port = lib.mkOption {
              type = lib.types.port;
              default = 8317;
              description = "Port to listen on.";
            };
          };
        };
      };
      example = lib.literalExpression ''
        {
          routing.strategy = "soonest-reset-first";
          observability.logs.request-log = false;
        }
      '';
    };

    taprootSettings = lib.mkOption {
      type = yaml.type;
      default = { };
      description = ''
        Taproot's own settings (access, backups, request logs, alerts),
        copied to `taproot.yaml` on the first start only, while that file
        does not exist. After that the dashboard owns the file, and changes
        here do not apply; edit the settings in the dashboard, or delete
        taproot.yaml to seed it again. Taproot does not start without that
        file, and the unit denies loopback, so this option must grant at
        least one device `admin: true` in `access.devices`; that device
        manages access from the dashboard. Keep it set after the first start.
        examples/taproot/taproot.yaml in the repository shows the keys.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/taproot.env";
      description = "File of environment variables for the service, such as MANAGEMENT_PASSWORD. It stays out of the Nix store.";
    };

    localModel = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Pass `--local-model`: use the embedded model catalogs instead of fetching the official remote ones.";
    };

    extraArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Extra command-line arguments for taproot.";
    };

    timeZone = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "Europe/Berlin";
      description = "TZ for the service, which sets the backup schedule's local time. Null uses the system time zone.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the port on the Tailscale interface only.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = !hostIsLocalOrWildcard;
        message = ''
          services.taproot.settings.server.host is "${cfg.settings.server.host}". Outside a container every
          local process can connect from loopback, and taproot treats loopback callers as admins, so
          taproot must listen on this host's Tailscale address. Set services.taproot.tailscale.address.
        '';
      }
      {
        assertion = !(lib.hasPrefix "/home/" cfg.dataDir || lib.hasPrefix "/root/" cfg.dataDir);
        message = "services.taproot.dataDir must be outside /home and /root, which the service cannot reach (ProtectHome).";
      }
      {
        # Format-agnostic, like the listen-address assertion: taproot validates
        # the access keys itself at startup.
        assertion = cfg.taprootSettings != { };
        message = ''
          services.taproot.taprootSettings is empty. It seeds taproot.yaml on the first start, and taproot
          does not start without that file. Grant at least one device admin = true in access.devices: the
          unit denies loopback, so only an admin device reaches the dashboard. It is not copied again while
          taproot.yaml exists.
        '';
      }
    ];

    services.taproot.settings = {
      config-version = lib.mkDefault 8;
      # taproot.yaml admits callers by Tailscale identity, so the proxy has no API keys.
      access = lib.mkDefault { };
      routing = {
        strategy = lib.mkDefault "soonest-reset-first";
        session-affinity = lib.mkDefault true;
        session-affinity-ttl = lib.mkDefault "1h";
      };
      observability.logs = {
        request-log = lib.mkDefault true;
        logging-to-file = lib.mkDefault false;
      };
      management = {
        # The access guard decides who reaches the management API and adds the key itself.
        allow-remote = lib.mkDefault true;
        disable-control-panel = lib.mkDefault true;
        disable-auto-update-panel = lib.mkDefault true;
      };
      oauth.auth-dir = lib.mkDefault "${cfg.dataDir}/auths";
    };

    users.users = lib.mkIf (cfg.user == "taproot") {
      taproot = {
        isSystemUser = true;
        inherit (cfg) group;
        home = cfg.dataDir;
      };
    };
    users.groups = lib.mkIf (cfg.group == "taproot") { taproot = { }; };

    systemd.tmpfiles.settings."10-taproot".${cfg.dataDir}.d = {
      inherit (cfg) user group;
      mode = "0700";
    };

    networking.firewall.interfaces.${config.services.tailscale.interfaceName}.allowedTCPPorts =
      lib.mkIf cfg.openFirewall
        [ cfg.settings.server.port ];

    systemd.services.taproot = {
      description = "taproot (tailnet-only AI proxy and dashboard)";
      # The access guard identifies callers through tailscaled's socket,
      # /run/tailscale/tailscaled.sock (taproot's default path,
      # /var/run/tailscale/tailscaled.sock, resolves to it).
      after = [
        "network-online.target"
        "tailscaled.service"
      ];
      wants = [
        "network-online.target"
        "tailscaled.service"
      ];
      wantedBy = [ "multi-user.target" ];

      environment = {
        HOME = "${cfg.dataDir}/home";
        XDG_CACHE_HOME = "${cfg.dataDir}/cache";
        WRITABLE_PATH = cfg.dataDir;
        # Resolve names through glibc, which reaches nscd or systemd-resolved
        # over unix sockets. Go's own resolver would send DNS to a loopback
        # stub, such as 127.0.0.53, which IPAddressDeny blocks.
        GODEBUG = "netdns=cgo";
      }
      // lib.optionalAttrs (cfg.timeZone != null) { TZ = cfg.timeZone; };

      preStart = ''
        install -m 0600 ${configFile} ${cfg.dataDir}/config.yaml
        if [ ! -e ${cfg.dataDir}/taproot.yaml ]; then
          install -m 0600 ${taprootFile} ${cfg.dataDir}/taproot.yaml
        fi
        mkdir -p ${cfg.dataDir}/home ${cfg.dataDir}/cache
      '';

      serviceConfig = {
        ExecStart = utils.escapeSystemdExecArgs ([ (lib.getExe cfg.package) ] ++ args);
        User = cfg.user;
        Group = cfg.group;
        WorkingDirectory = cfg.dataDir;
        EnvironmentFile = lib.optional (cfg.environmentFile != null) cfg.environmentFile;
        # Restarts also cover a start before tailscaled has assigned the address.
        Restart = "always";
        RestartSec = 5;
        # Taproot closes its connections at once on stop, so streams in flight
        # are cut, not drained. This only bounds a stop that hangs.
        TimeoutStopSec = 45;
        UMask = "0077";

        # Every local process can connect from loopback, and taproot treats
        # loopback callers as admins, so loopback traffic is dropped in both
        # directions. Outbound connections to loopback, such as a local
        # proxy-url or DNS stub, are dropped too.
        IPAddressDeny = "localhost";
        # AF_UNIX for tailscaled's socket and name lookups, AF_NETLINK for
        # Go's interface listing.
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
          "AF_NETLINK"
        ];

        ReadWritePaths = [ cfg.dataDir ];
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        AmbientCapabilities = "";
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "@system-service"
          "~@privileged"
        ];
      };
    };
  };
}
