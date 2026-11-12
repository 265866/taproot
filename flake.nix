{
  description = "Taproot: a tailnet-only AI proxy with quota tracking, request logs, and a dashboard";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    bun2nix = {
      url = "github:nix-community/bun2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      bun2nix,
    }:
    let
      inherit (nixpkgs) lib;
      # Linux only: taproot identifies callers through tailscaled's unix socket.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # The flake has no git tags to describe, so the version is package.json's
      # with the commit date, and the commit is the flake's revision.
      buildInfo =
        let
          # YYYYMMDDHHMMSS, the commit time.
          at = start: len: builtins.substring start len (self.lastModifiedDate or "19700101000000");
          date = "${at 0 4}-${at 4 2}-${at 6 2}";
        in
        {
          version = "${(lib.importJSON ./web/package.json).version}-unstable-${date}";
          commit = self.shortRev or self.dirtyShortRev or "dirty";
          buildDate = "${date}T${at 8 2}:${at 10 2}:${at 12 2}Z";
        };

      packagesFor =
        pkgs:
        let
          taproot-codec = pkgs.callPackage ./nix/codec.nix { };
          taproot-web = pkgs.callPackage ./nix/web.nix {
            inherit (buildInfo) version;
            bun2nix = bun2nix.packages.${pkgs.stdenv.hostPlatform.system}.default;
          };
          taproot = pkgs.callPackage ./nix/taproot.nix {
            inherit buildInfo taproot-codec taproot-web;
          };
        in
        {
          inherit taproot taproot-codec taproot-web;
        };
    in
    {
      packages = forAllSystems (
        pkgs:
        packagesFor pkgs
        // {
          default = self.packages.${pkgs.stdenv.hostPlatform.system}.taproot;
        }
      );

      overlays.default = final: _prev: packagesFor final;

      nixosModules.taproot =
        { pkgs, ... }:
        {
          imports = [ ./nix/module.nix ];
          services.taproot.package = lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.taproot;
        };
      nixosModules.default = self.nixosModules.taproot;

      checks = forAllSystems (pkgs: {
        inherit (self.packages.${pkgs.stdenv.hostPlatform.system}) taproot;
        module-assertions = import ./nix/assertions.nix {
          inherit lib pkgs;
          inherit (lib) nixosSystem;
          module = self.nixosModules.taproot;
        };
        nixos = pkgs.testers.runNixOSTest {
          imports = [ ./nix/test.nix ];
          defaults.imports = [ self.nixosModules.taproot ];
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go_1_26
            pkgs.gofumpt
            pkgs.bun
            pkgs.cargo
            pkgs.rustc
            pkgs.clippy
            pkgs.rustfmt
            bun2nix.packages.${pkgs.stdenv.hostPlatform.system}.default
          ];
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
