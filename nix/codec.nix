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
      # The LGPL relinking steps in THIRD_PARTY_NOTICES.md put a patched cabac
      # crate here, as the image build copies it.
      (root + "/patches")
    ];
  };

  cargoLock.lockFile = root + "/Cargo.lock";

  # nixpkgs' cargo hook points zstd-sys at a system libzstd. Build the bundled
  # zstd into the library instead, as the image does: the server's cgo flags
  # link no libzstd.
  preBuild = ''
    unset ZSTD_SYS_USE_PKG_CONFIG
  '';

  # cargoInstallHook copies libtaproot_codec.a to $out/lib. Go 1.26.11 refuses
  # to link LLVM bitcode sections, which the precompiled compiler_builtins and
  # zstd's assembly add; removing them leaves the native code.
  postInstall = ''
    ''${OBJCOPY:-objcopy} --remove-section=.llvmbc --remove-section=.llvmcmd $out/lib/libtaproot_codec.a
  '';
  meta = {
    description = "Taproot's request-log blob codecs (Lepton JPEG, PNG recompression, zstd) as a static library";
    # MIT, plus the LGPL-3.0-or-later cabac crate built into the library.
    license = with lib.licenses; [
      mit
      lgpl3Plus
    ];
    platforms = lib.platforms.linux;
  };
}
