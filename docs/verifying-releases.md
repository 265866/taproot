# Verify a release

The release workflow in this repository, [`release.yml`](../.github/workflows/release.yml), builds every release. Each image has a GitHub build attestation and a keyless cosign signature, and each tarball has a GitHub build attestation. This page shows how to check them before you run a release.

The commands read the release's version from the shell variable `v`, such as `0.2.0`. [Choose the release](install.md#choose-the-release) shows how to set it to the latest release. Each check names the workflow and the tag the release was built from:

```sh
identity='^https://github\.com/265866/taproot/\.github/workflows/release\.yml@refs/tags/taproot-v'
line="${v%.*}"
```

Image tags are `X.Y.Z` for one release, `X.Y` for the newest release of a minor version, and `latest` for the newest release.

## Verify an image

Verify the digest of the image you pulled, not its tag, because a tag can move between the pull and the check. Install [cosign](https://github.com/sigstore/cosign/releases) first, then run:

```sh
podman pull "ghcr.io/265866/taproot:$line"
ref=$(podman image inspect --format '{{index .RepoDigests 0}}' "ghcr.io/265866/taproot:$line")
cosign verify "$ref" \
  --certificate-identity-regexp "$identity${line//./\\.}\.[0-9]+\$" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The identity names the release line, so an image signed for a prerelease or another minor version does not pass. The release signs the multi-arch index and each platform's image, so the check passes on whichever digest Podman recorded. With Docker, run `docker pull` and `docker image inspect` with the same arguments instead.

To check the image's build attestation, install the [GitHub CLI](https://cli.github.com/) and run:

```sh
gh attestation verify "oci://ghcr.io/265866/taproot:$v" --repo 265866/taproot \
  --cert-identity-regex "$identity${v//./\\.}\$"
```

The attestation covers only the multi-arch index, which the `X.Y.Z` tag names. It does not cover the platform digest that Podman can record.

## Verify a tarball

Each release has a tarball for linux-amd64 and linux-arm64, a `SHA256SUMS` file, and an attestation. You need the [GitHub CLI](https://cli.github.com/).

```sh
arch=amd64   # or arm64
gh release download "taproot-v$v" --repo 265866/taproot \
  --pattern "taproot-$v-linux-$arch.tar.gz" --pattern SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
gh attestation verify "taproot-$v-linux-$arch.tar.gz" --repo 265866/taproot \
  --cert-identity-regex "$identity${v//./\\.}\$"
```

The `sha256sum` and `gh attestation verify` checks must both pass. [Plain binary with systemd](install.md#plain-binary-with-systemd) shows how to install the tarball.
