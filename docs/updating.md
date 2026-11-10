# Update taproot

Updates are manual unless you turn on the automatic updater. A restart cuts the requests that are running through taproot, so update when your agents are idle.

## Install a patch release

A minor version's image tag, such as `0.3`, brings its patch releases and never a new minor version. Replace `0.3` below with the tag your setup runs.

| Setup | Update |
| --- | --- |
| Quick start container | `podman pull ghcr.io/265866/taproot:0.5`, then `podman rm -f taproot` and run the [Quick start](../README.md#quick-start) command again |
| Podman quadlet | `podman pull ghcr.io/265866/taproot:0.5 && systemctl --user restart taproot` |
| Docker Compose | `docker compose pull && docker compose up -d` |
| NixOS | Change the tag in `inputs.taproot.url` to the new release, then run `nix flake update taproot` and `nixos-rebuild switch` |
| Plain binary | Download and check the new tarball as in [Verify a tarball](verifying-releases.md#verify-a-tarball), unpack it, point `/opt/taproot` at it, and run `sudo systemctl restart taproot` |

## Move to a new minor version

A minor version can rename `taproot.yaml` keys, so read its release notes in [CHANGELOG.md](../CHANGELOG.md) first. [Stability](stability.md) says what a minor version can change.

1. Stop taproot.
2. Copy the whole data directory. Only this copy lets you roll back to the old minor version later.
3. Change the `taproot.yaml` keys that the release notes list.
4. Change the image tag, the flake tag, or the tarball to the new minor version, and start taproot.

   With the automatic updater, leave the image tag alone. Change `TAPROOT_TRACK` in `~/.config/taproot-update.env`, run `~/.local/bin/taproot-update`, which verifies the new release and tags it while taproot is stopped, then run `systemctl --user start taproot`.

## Roll back

A rollback runs an older release on data that a newer one wrote.

Within one minor version, such as from `0.3.2` back to `0.3.0`, run the older release on the same data directory. If the newer patch release added a `taproot.yaml` key that you set, remove the key first, because the older release refuses keys that it does not know.

To roll back across minor versions, such as from `0.3.0` to `0.2.4`, you need the copy from step 2 of [Move to a new minor version](#move-to-a-new-minor-version):

1. Stop taproot.
2. Replace the data directory with the copy. Keep the current `auths/` directory instead of the one in the copy, because taproot refreshes OAuth tokens while it runs and the copied tokens may no longer work.
3. Start the older release.

The usage history, quota history, and request logs recorded after the copy are lost.

## Move from 0.1.0

Later releases do not read the database, request logs, or backups of `taproot-v0.1.0`, and no release converts them.

1. Stop 0.1.0, so that two servers never refresh the same OAuth tokens.
2. Create a new, empty data directory.
3. Copy `config.yaml`, `taproot.yaml`, and the `auths/` directory into it.
4. In `taproot.yaml`, replace `access.allow-logins`, `access.admin-nodes`, and `access.trusted-cidrs` with an `access.devices` list and `access.addresses`, as [`examples/taproot/taproot.yaml`](../examples/taproot/taproot.yaml) shows. Taproot refuses to start until you do.
5. Start the new release on the new directory.

The usage history, quota history, request logs, and backups of 0.1.0 stay in the old directory.

## Turn on automatic updates

With the [Podman quadlet](install.md#podman-quadlet), [`scripts/taproot/taproot-update`](../scripts/taproot/taproot-update) can install patch releases for you every night at about 04:00. It follows one minor version, set by `TAPROOT_TRACK`, so patch releases install and a new minor version never does. It installs a release only when cosign verifies that this repository's release workflow signed it for a tag on that line. It pulls before it restarts taproot, which leaves taproot unavailable for about half a second (measured), and it goes back to the previous image when the new release does not answer `/healthz` within two minutes.

The updater does not pull the quadlet's `ghcr.io` tag. It moves a local tag, `localhost/taproot:current`, so the quadlet must run that tag.

1. Set `v` to the release your quadlet runs, as in [Choose the release](install.md#choose-the-release). Install the script, its settings file [`taproot-update.env`](../examples/taproot/taproot-update.env), and its units [`taproot-update.service`](../examples/taproot/taproot-update.service) and [`taproot-update.timer`](../examples/taproot/taproot-update.timer) from that release tag, and set `TAPROOT_HEALTH_URL` and `TAPROOT_TRACK`:

   ```sh
   base="https://raw.githubusercontent.com/265866/taproot/taproot-v$v"
   mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user"
   curl -fsSL -o "$HOME/.local/bin/taproot-update" "$base/scripts/taproot/taproot-update"
   chmod 0755 "$HOME/.local/bin/taproot-update"
   curl -fsSL -o "$HOME/.config/taproot-update.env" "$base/examples/taproot/taproot-update.env"
   for unit in taproot-update.service taproot-update.timer; do
     curl -fsSL -o "$HOME/.config/systemd/user/$unit" "$base/examples/taproot/$unit"
   done
   sed -i -e "s|^TAPROOT_HEALTH_URL=http://100\.64\.0\.1:|TAPROOT_HEALTH_URL=http://$(tailscale ip -4):|" \
     -e "s|^TAPROOT_TRACK=.*|TAPROOT_TRACK=${v%.*}|" \
     "$HOME/.config/taproot-update.env"
   ```

2. See what the updater would do. This command reads the settings, finds the newest release on the line, and changes nothing:

   ```sh
   ~/.local/bin/taproot-update --check
   ```

3. Point the quadlet at the local tag, run the updater once, and start its timer only when that run succeeds. Taproot keeps running the `ghcr.io` image until the updater restarts it:

   ```sh
   sed -i 's|^Image=.*|Image=localhost/taproot:current\nPull=never|' \
     "$HOME/.config/containers/systemd/taproot.container"
   systemctl --user daemon-reload
   ~/.local/bin/taproot-update && systemctl --user enable --now taproot-update.timer
   ```

   The first run finds no local tag. It verifies the newest release on the line, pulls it by digest, tags it as `localhost/taproot:current`, restarts taproot on it, and waits for `/healthz`. Do not tag the image yourself. The updater skips a release that the local tag already points to, so it never verifies a release you tagged by hand.

   If the first run fails, the local tag may not exist yet, and then taproot does not come back after its next restart. Or the tag may point to a release that failed its health check, with no previous image to roll back to. In both cases, put the quadlet back on the `ghcr.io` tag until you fix the cause:

   ```sh
   line="$(sed -n 's/^TAPROOT_TRACK=//p' "$HOME/.config/taproot-update.env")"
   sed -i -e "s|^Image=localhost/taproot:current\$|Image=ghcr.io/265866/taproot:$line|" -e '/^Pull=never$/d' \
     "$HOME/.config/containers/systemd/taproot.container"
   systemctl --user daemon-reload
   systemctl --user restart taproot
   ```

`journalctl --user -u taproot-update` shows each run. The first run downloads cosign to `~/.local/share/taproot-update/` and checks it against a pinned SHA-256. `taproot-update --help` lists its settings. To update at once instead of waiting for the timer, run `~/.local/bin/taproot-update`.
