# The dashboard build: index.html and the hashed chunks under assets/, which
# the server embeds from internal/taproot/web/dist. web/bun.nix lists every
# package in web/bun.lock; scripts/taproot/nix-refresh.sh regenerates it.
{
  lib,
  stdenvNoCC,
  bun2nix,
  version,
}:
let
  root = ../web;
in
stdenvNoCC.mkDerivation {
  pname = "taproot-web";
  inherit version;

  src = lib.fileset.toSource {
    inherit root;
    fileset = lib.fileset.difference root (
      lib.fileset.unions [
        (lib.fileset.maybeMissing (root + "/node_modules"))
        (lib.fileset.maybeMissing (root + "/dist"))
        (root + "/bun.nix")
      ]
    );
  };

  nativeBuildInputs = [ bun2nix.hook ];

  bunDeps = bun2nix.fetchBunDeps { bunNix = root + "/bun.nix"; };
  # The layout `bun install` gives web/ everywhere else. The sources import
  # some packages they get only through hoisting, such as react-router.
  bunInstallFlags = [ "--linker=hoisted" ];

  # vite.config.ts takes the version from VERSION, and otherwise runs git
  # describe, which has no repository here.
  env.VERSION = version;

  dontUseBunBuild = true;
  dontUseBunCheck = true;
  dontUseBunInstall = true;

  buildPhase = ''
    runHook preBuild
    bun run build
    runHook postBuild
  '';

  # The server serves index.html and assets/ only, as in scripts/taproot/build-web.sh.
  installPhase = ''
    runHook preInstall
    extra="$(find dist -type f ! -path dist/index.html ! -path 'dist/assets/*')"
    if [ -n "$extra" ]; then
      printf 'web/dist has files outside index.html and assets/:\n%s\n' "$extra" >&2
      exit 1
    fi
    mkdir -p $out
    cp dist/index.html $out/index.html
    cp -r dist/assets $out/assets
    runHook postInstall
  '';

  meta = {
    description = "Taproot's dashboard, built for embedding in the server";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
  };
}
