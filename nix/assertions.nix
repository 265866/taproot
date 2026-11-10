# Evaluation-only check: the module accepts only a literal Tailscale listen
# address, brackets an IPv6 tailscale.address, refuses empty taprootSettings,
# warns about a webhook in taprootSettings, and loads discordWebhookFile as a
# credential.
{
  lib,
  pkgs,
  nixosSystem,
  module,
}:
let
  adminSettings.access.devices = [
    {
      id = "nADMIN";
      name = "laptop";
      admin = true;
    }
  ];

  configFor =
    taproot:
    (nixosSystem {
      inherit (pkgs.stdenv.hostPlatform) system;
      modules = [
        module
        {
          services.taproot = {
            enable = true;
            taprootSettings = lib.mkDefault adminSettings;
          }
          // taproot;
          boot.isContainer = true;
          system.stateVersion = lib.trivial.release;
        }
      ];
    }).config;

  # The messages of the assertions that fail for a host with these taproot
  # options. Only failed assertions' messages are safe to read: some modules
  # build a message from values that exist only when their assertion fails.
  failedFor =
    taproot: map (a: a.message) (lib.filter (a: !a.assertion) (configFor taproot).assertions);

  # Whether exactly the assertion whose message starts with prefix fails, or
  # none does; any other failure stops the check.
  refusedBy =
    prefix: name: taproot:
    let
      failed = failedFor taproot;
    in
    if failed == [ ] then
      false
    else if lib.all (lib.hasPrefix prefix) failed then
      true
    else
      throw "unexpected failed assertions for ${name}: ${lib.concatStringsSep "; " failed}";

  hostAllowed =
    host:
    !refusedBy "services.taproot.settings.server.host" ''host "${host}"'' {
      settings.server.host = host;
    };

  settingsRefused =
    name: settings:
    refusedBy "services.taproot.taprootSettings" name {
      settings.server.host = "100.64.0.1";
      taprootSettings = settings;
    };

  refused = [
    ""
    "0.0.0.0"
    "[::]"
    "LOCALHOST"
    "127.0.1.1"
    "::1"
    "::ffff:127.0.0.1"
    "taproot.localhost"
    # glibc's resolver reads these as the wildcard and loopback addresses.
    "0"
    "0x7f.1"
    "100.64.1"
    # Octal to inet_aton.
    "100.064.0.1"
    "100.128.0.1"
    "100.64.0.256"
    "192.0.2.1"
    "server.tailnet-0000.ts.net"
    # Upstream listens on "host:port", which needs the brackets.
    "fd7a:115c:a1e0::1"
    "[fd7a:115c:a1e1::1]"
  ];
  accepted = [
    "100.64.0.1"
    "100.127.255.254"
    "[fd7a:115c:a1e0::1]"
    "[FD7A:115C:A1E0:AB12:4843:CD96:6258:B240]"
  ];
  wrongHosts = lib.filter hostAllowed refused ++ lib.filter (host: !hostAllowed host) accepted;

  # An IPv6 tailscale.address becomes a bracketed listen address.
  ipv6Default = configFor { tailscale.address = "fd7a:115c:a1e0::1"; };
  wrongDefault = lib.optional (
    failedFor { tailscale.address = "fd7a:115c:a1e0::1"; } != [ ]
    || ipv6Default.services.taproot.settings.server.host != "[fd7a:115c:a1e0::1]"
  ) "an IPv6 tailscale.address";

  # The webhook file reaches the unit as a credential, never by its content.
  webhookFile = configFor {
    tailscale.address = "100.64.0.1";
    discordWebhookFile = "/run/secrets/taproot-discord-webhook";
  };
  webhookInStore = configFor {
    tailscale.address = "100.64.0.1";
    taprootSettings = adminSettings // {
      alerts.discord-webhook = "https://discord.com/api/webhooks/0/example";
    };
  };
  wrongWebhook =
    lib.optional (
      webhookFile.systemd.services.taproot.serviceConfig.LoadCredential != [
        "discord-webhook:/run/secrets/taproot-discord-webhook"
      ]
      || !lib.hasInfix "$CREDENTIALS_DIRECTORY/discord-webhook" webhookFile.systemd.services.taproot.preStart
    ) "discordWebhookFile"
    ++ lib.optional (
      !lib.any (lib.hasInfix "taprootSettings.alerts.discord-webhook") webhookInStore.warnings
    ) "a webhook in taprootSettings";

  wrongSettings =
    lib.optional (!settingsRefused "empty taprootSettings" { }) "empty taprootSettings"
    ++ lib.optional (settingsRefused "an admin device" adminSettings) "taprootSettings with an admin device";
  wrong = map (h: ''host "${h}"'') wrongHosts ++ wrongSettings ++ wrongDefault ++ wrongWebhook;
in
if wrong == [ ] then
  pkgs.runCommand "taproot-module-assertions" { } "touch $out"
else
  throw "services.taproot assertion is wrong for: ${lib.concatStringsSep ", " wrong}"
