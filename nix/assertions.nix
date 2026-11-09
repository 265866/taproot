# Evaluation-only check: the module refuses a listen address that is
# loopback or a wildcard, accepts a Tailscale address, and refuses empty
# taprootSettings.
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

  # The messages of the assertions that fail for a host with these taproot
  # options. Only failed assertions' messages are safe to read: some modules
  # build a message from values that exist only when their assertion fails.
  failedFor =
    taproot:
    let
      inherit
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
        })
        config
        ;
    in
    map (a: a.message) (lib.filter (a: !a.assertion) config.assertions);

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
  ];
  accepted = [
    "100.64.0.1"
    "fd7a:115c:a1e0::1"
  ];
  wrongHosts = lib.filter hostAllowed refused ++ lib.filter (host: !hostAllowed host) accepted;

  wrongSettings =
    lib.optional (!settingsRefused "empty taprootSettings" { }) "empty taprootSettings"
    ++ lib.optional (settingsRefused "an admin device" adminSettings) "taprootSettings with an admin device";
in
if wrongHosts == [ ] && wrongSettings == [ ] then
  pkgs.runCommand "taproot-module-assertions" { } "touch $out"
else
  throw "services.taproot assertion is wrong for: ${
    lib.concatStringsSep ", " (map (h: ''host "${h}"'') wrongHosts ++ wrongSettings)
  }"
