# Install taproot

This page shows each way to run taproot. The setups below start at boot, unlike the rootless Podman [Quick start](../README.md#quick-start) container, and all of them use the same [data directory](configuration.md#the-data-directory).

## Requirements

- A Linux host, amd64 or arm64, on your tailnet, with `tailscaled` running.
- A runtime that keeps each caller's source address: rootless Podman 5 or later, rootful Docker, or no container. Rootless Docker does not work in its default setup, because it shows taproot every caller as the Docker bridge's gateway address.
- A second device on the same tailnet, with a browser, to claim the server and open the dashboard.

Taproot runs on Linux only. It identifies callers through the host's `tailscaled` socket, and Docker Desktop on macOS and Windows has no such socket and does not keep caller addresses.

After you install, claim the server with the setup code, as in step 2 of the [Quick start](../README.md#quick-start) (on NixOS, `taprootSettings` gives Admin instead), and run [Check the deployment](access.md#check-the-deployment).

## Choose the release

The commands on this page download files from a release tag, because `main` can hold unreleased changes. They read the version from a shell variable, `v`. Set it to the latest release:

```sh
v=$(curl -fsSL https://api.github.com/repos/265866/taproot/releases/latest \
  | sed -n 's/.*"tag_name": *"taproot-v\([^"]*\)".*/\1/p')
echo "$v"
```

`${v%.*}` is the release's minor version, such as `0.2`, which is also the image tag that follows its patch releases.

## Keep tailscaled's folder for a container

A container reaches `tailscaled` through the host's `/var/run/tailscale` folder, which the Quick start container, the quadlet, and the Compose file mount. Run this once on the host before you start one:

```sh
sudo mkdir -p /etc/systemd/system/tailscaled.service.d
printf '[Service]\nRuntimeDirectoryPreserve=yes\n' | sudo tee /etc/systemd/system/tailscaled.service.d/taproot.conf
sudo systemctl daemon-reload
```

`tailscaled` does not need a restart now; the setting applies from its next restart. Without it, systemd deletes and recreates the folder each time `tailscaled` restarts or updates, and the running container keeps the deleted one, so taproot refuses every tailnet device until you restart it. NixOS and the plain binary run without a container and do not need this.

## Run the Quick start container with Docker

The [Quick start](../README.md#quick-start) command works with rootful Docker too, after the host setting in [Keep tailscaled's folder for a container](#keep-tailscaleds-folder-for-a-container). Run it with `docker run`, leave out `--network pasta`, and add `--user "$(id -u):$(id -g)"`, so the process runs as the owner of the data directory. Find the setup code with `docker logs taproot 2>&1 | grep 'setup code [A-Z0-9]'`.

On the first start, taproot writes `config.yaml` and `taproot.yaml` into `~/taproot/data`. `--network pasta` keeps each caller's source address. `TZ` sets the time of the daily backup. On an SELinux host, add `:Z` to the data volume, as in `-v "$HOME/taproot/data:/taproot/config:Z"`.

Rootless Podman does not start this container again after the host reboots. For a container that does, use the quadlet or Compose setup below with the same data directory.

## Podman quadlet

[`examples/taproot/taproot.container`](../examples/taproot/taproot.container) runs taproot as a systemd user service under rootless Podman. It uses a read-only root filesystem, drops every capability, and publishes the port on the host's Tailscale address only. If you started the Quick start container, run `podman rm -f taproot` first. Before you start it, make the host setting in [Keep tailscaled's folder for a container](#keep-tailscaleds-folder-for-a-container).

```sh
mkdir -p "$HOME/taproot/data" "$HOME/.config/containers/systemd"
curl -fsSL -o "$HOME/.config/containers/systemd/taproot.container" \
  "https://raw.githubusercontent.com/265866/taproot/taproot-v$v/examples/taproot/taproot.container"
sed -i "s/^PublishPort=100\.64\.0\.1:/PublishPort=$(tailscale ip -4):/" \
  "$HOME/.config/containers/systemd/taproot.container"
podman pull "ghcr.io/265866/taproot:${v%.*}"
systemctl --user daemon-reload
systemctl --user start taproot
loginctl enable-linger "$USER"
journalctl --user -u taproot | grep 'setup code [A-Z0-9]'
```

The quadlet runs the minor version's tag, such as `ghcr.io/265866/taproot:0.3`, and pulls it only when the host does not have it, so an update waits until you pull. `loginctl enable-linger` keeps the service running when you are not signed in. Run the quadlet as your own user, without `User=` or `UserNS=`. Podman maps the container's root to your user, which owns the files in the data directory.

## Docker Compose

[`examples/taproot/compose.yaml`](../examples/taproot/compose.yaml) runs taproot under rootful Docker with the same restrictions as the quadlet. It reads the host's Tailscale address and your user and group IDs from a `.env` file beside it. Before you start it, make the host setting in [Keep tailscaled's folder for a container](#keep-tailscaleds-folder-for-a-container).

```sh
mkdir -p "$HOME/taproot/data"
cd "$HOME/taproot"
curl -fsSLO "https://raw.githubusercontent.com/265866/taproot/taproot-v$v/examples/taproot/compose.yaml"
printf 'TS_IP=%s\nTAPROOT_USER=%s:%s\n' "$(tailscale ip -4)" "$(id -u)" "$(id -g)" > .env
docker compose up -d
docker compose logs taproot | grep 'setup code [A-Z0-9]'
```

Create `data/` before the first `docker compose up`. Otherwise Docker creates it as root, and taproot cannot write to it.

## NixOS

The flake's NixOS module, `services.taproot`, builds taproot from source and runs it as a hardened systemd service after `tailscaled.service`. Add the flake to your system flake and enable the module. Replace `<version>` with the [latest release](https://github.com/265866/taproot/releases/latest), such as `0.2.0`:

```nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  # A release tag. Without one, the input follows unreleased commits on main.
  inputs.taproot.url = "github:265866/taproot/taproot-v<version>";
  inputs.taproot.inputs.nixpkgs.follows = "nixpkgs";

  outputs = { nixpkgs, taproot, ... }: {
    nixosConfigurations.server = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        taproot.nixosModules.default
        {
          services.tailscale.enable = true;
          services.taproot = {
            enable = true;
            # This host's address from `tailscale ip -4`. Taproot listens only there.
            tailscale.address = "100.64.0.1";
            openFirewall = true;
            timeZone = "Etc/UTC";
            # Copied to taproot.yaml on the first start only.
            taprootSettings.access.devices = [
              { id = "nEXAMPLE1CNTRL"; name = "laptop"; proxy = true; admin = true; }
            ];
          };
        }
      ];
    };
  };
}
```

The module keeps loopback callers out and requires `taprootSettings`, so give Admin there to the device you manage taproot from. Its stable ID is the `ID` that `tailscale status --json` prints under `Self` on that device.

With `follows`, taproot builds with your nixpkgs, which must have at least the Go version on the `go` line of taproot's `go.mod`. An older one stops the evaluation with a message that names both versions. Update your nixpkgs input, or remove the `follows` line so that taproot uses its own locked nixpkgs.

Nix owns `config.yaml` and writes it from `services.taproot.settings` on every start, so a dashboard change to it lasts until the next restart. Nix seeds `taproot.yaml` from `taprootSettings` only while the file does not exist. After that the dashboard owns `taproot.yaml`, and later changes to `taprootSettings` do not apply. The data directory is `/var/lib/taproot`. [`nix/module.nix`](../nix/module.nix) documents every option.

## Plain binary with systemd

Each release has a tarball for linux-amd64 and linux-arm64. The binary needs glibc 2.34 or later, so it runs on Debian 12, Ubuntu 22.04, and later, but not on musl distributions such as Alpine. To download and check the tarball for release `$v` and architecture `$arch` (`amd64` or `arm64`), follow [Verify a tarball](verifying-releases.md#verify-a-tarball), then unpack it:

```sh
sudo tar -xzf "taproot-$v-linux-$arch.tar.gz" -C /opt
sudo ln -sfn "/opt/taproot-$v-linux-$arch" /opt/taproot
```

The tarball holds the `taproot` binary with `LICENSE`, `THIRD_PARTY_NOTICES.md`, `web/LICENSE`, and `licenses/`. Keep those files beside the binary. The binary includes LGPL-licensed code, and the LGPL requires its notices to go with every copy you pass on.

Outside a container, every process on the host can connect from loopback, and loopback callers are admins. For that reason, [`examples/taproot/taproot.service`](../examples/taproot/taproot.service) drops loopback traffic, and taproot must listen on the host's Tailscale address. Write the configuration before the first start:

```sh
sudo useradd --system --home-dir /var/lib/taproot --shell /usr/sbin/nologin taproot
sudo install -d -m 0700 -o taproot -g taproot /var/lib/taproot
curl -fsSL "https://raw.githubusercontent.com/265866/taproot/taproot-v$v/examples/taproot/config.yaml" \
  | sed -e "s|host: \"0.0.0.0\"|host: \"$(tailscale ip -4)\"|" \
        -e 's|/taproot/config/auths|/var/lib/taproot/auths|' \
  | sudo install -m 0600 -o taproot -g taproot /dev/stdin /var/lib/taproot/config.yaml
sudo install -m 0600 -o taproot -g taproot /dev/null /var/lib/taproot/taproot.yaml
sudo curl -fsSL -o /etc/systemd/system/taproot.service \
  "https://raw.githubusercontent.com/265866/taproot/taproot-v$v/examples/taproot/taproot.service"
sudo systemctl daemon-reload
sudo systemctl enable --now taproot
sudo journalctl -u taproot | grep 'setup code [A-Z0-9]'
```

A browser on the server itself connects as the server's own node, which never gets Admin. Claim the server from another device.
