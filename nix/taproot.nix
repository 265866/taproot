# The server, built like Containerfile.taproot: the dashboard embedded, the
# codec library linked with the taproot_codecs tag, and the version ldflags.
{
  lib,
  buildGo126Module,
  buildInfo,
  taproot-codec,
  taproot-web,
}:
let
  root = ./..;
in
buildGo126Module {
  pname = "taproot";
  inherit (buildInfo) version;

  src = lib.fileset.toSource {
    inherit root;
    fileset =
      lib.fileset.difference
        (lib.fileset.unions [
          (root + "/go.mod")
          (root + "/go.sum")
          (root + "/cmd")
          (root + "/internal")
          (root + "/sdk")
          (root + "/LICENSE")
          (root + "/THIRD_PARTY_NOTICES.md")
          (root + "/web/LICENSE")
          (root + "/licenses")
        ])
        (
          lib.fileset.unions [
            (lib.fileset.maybeMissing (root + "/internal/taproot/web/dist/assets"))
            (lib.fileset.maybeMissing (root + "/internal/taproot/reqlog/codec/native/target"))
          ]
        );
  };

  # go.sum decides it; scripts/taproot/nix-refresh.sh updates it.
  vendorHash = lib.fileContents ./vendor-hash;

  subPackages = [ "cmd/server" ];
  tags = [ "taproot_codecs" ];
  env.CGO_ENABLED = 1;

  ldflags = [
    "-s"
    "-w"
    "-X main.Version=${buildInfo.version}"
    "-X main.Commit=${buildInfo.commit}"
    "-X main.BuildDate=${buildInfo.buildDate}"
  ];

  # The embedded dashboard and the static library cgo links. The module fetch
  # needs neither, so vendorHash does not change with them.
  preBuild = ''
    rm -rf internal/taproot/web/dist
    mkdir -p internal/taproot/web/dist internal/taproot/reqlog/codec/native/target/release
    cp -r ${taproot-web}/. internal/taproot/web/dist/
    cp ${taproot-codec}/lib/libtaproot_codec.a internal/taproot/reqlog/codec/native/target/release/
  '';
  overrideModAttrs = _: { preBuild = ""; };

  # The codec statically links an LGPL-3.0 crate, so the licenses and notices
  # ship with the binary, at the relative paths THIRD_PARTY_NOTICES.md links to.
  postInstall = ''
    mv $out/bin/server $out/bin/taproot
    doc=$out/share/doc/taproot
    install -Dm644 -t $doc LICENSE THIRD_PARTY_NOTICES.md
    install -Dm644 -t $doc/web web/LICENSE
    install -Dm644 -t $doc/licenses licenses/*
  '';

  passthru = { inherit taproot-codec taproot-web; };

  meta = {
    description = "Tailnet-only AI proxy with quota tracking, request logs, backups, and a dashboard";
    homepage = "https://github.com/265866/taproot";
    # MIT, plus the LGPL-3.0-or-later cabac crate linked into the codec.
    license = with lib.licenses; [
      mit
      lgpl3Plus
    ];
    mainProgram = "taproot";
    platforms = lib.platforms.linux;
  };
}
