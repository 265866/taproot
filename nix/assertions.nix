# Evaluation-only check: the module refuses a listen address that is
# loopback or a wildcard, and accepts a Tailscale address.
{
  lib,
  pkgs,
  nixosSystem,
  module,
}:
let
  hostAllowed =
    host:
    let
      inherit
        (nixosSystem {
          inherit (pkgs.stdenv.hostPlatform) system;
          modules = [
            module
            {
              services.taproot.enable = true;
              services.taproot.settings.server.host = host;
              boot.isContainer = true;
              system.stateVersion = lib.trivial.release;
            }
          ];
        })
        config
        ;
      # Only failed assertions' messages are safe to read: some modules build
      # a message from values that exist only when their assertion fails.
      failed = map (a: a.message) (lib.filter (a: !a.assertion) config.assertions);
    in
    if failed == [ ] then
      true
    else if lib.all (lib.hasPrefix "services.taproot.settings.server.host") failed then
      false
    else
      throw "unexpected failed assertions for host \"${host}\": ${lib.concatStringsSep "; " failed}";

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
  wrong = lib.filter hostAllowed refused ++ lib.filter (host: !hostAllowed host) accepted;
in
if wrong == [ ] then
  pkgs.runCommand "taproot-module-assertions" { } "touch $out"
else
  throw "services.taproot host assertion is wrong for: ${
    lib.concatMapStringsSep ", " (h: ''"${h}"'') wrong
  }"
