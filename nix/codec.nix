# The request-log codec library (Lepton, preflate, zstd): a Rust static
# library that the server links with the taproot_codecs build tag.
{
  lib,
  rustPlatform,
  version,
}:
let
  root = ../internal/taproot/reqlog/codec/native;
in
rustPlatform.buildRustPackage {
  pname = "taproot-codec";
  inherit version;

  src = lib.fileset.toSource {
    inherit root;
    fileset = lib.fileset.unions [
      (root + "/Cargo.toml")
      (root + "/Cargo.lock")
      (root + "/src")
    ];
  };

  cargoLock.lockFile = root + "/Cargo.lock";

  # nixpkgs' cargo hook points zstd-sys at a system libzstd. Build the bundled
  # zstd into the library instead, as the image does: the server's cgo flags
  # link no libzstd.
  preBuild = ''
    unset ZSTD_SYS_USE_PKG_CONFIG
  '';

  # cargoInstallHook copies libtaproot_codec.a to $out/lib.
  meta = {
    description = "Taproot's request-log blob codecs (Lepton JPEG, PNG recompression, zstd) as a static library";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
  };
}
