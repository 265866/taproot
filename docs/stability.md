# Stability and upgrades

Taproot is not at version 1.0 yet. This page says what a release can change, what stays compatible, and how to upgrade and roll back.

## A minor release can break things

Releases are tagged `taproot-vX.Y.Z`. Before 1.0, the version numbers mean this:

- A minor release, such as `0.2.0` to `0.3.0`, can break things. It can rename or remove a `taproot.yaml` key, change the `/taproot/api/v1` API, or change how the dashboard works.
- A patch release, such as `0.2.0` to `0.2.1`, fixes bugs.

Each release's notes, in [CHANGELOG.md](../CHANGELOG.md) and on the GitHub release, say what changed and what you must do before you upgrade. Read them before every minor upgrade.

## Database changes only add tables and columns

Taproot keeps usage, quota, and the request-log index in `taproot/taproot.db` in the data directory. A release changes that database only by adding tables, indexes, or columns. It never renames or drops a table or a column.

Taproot does not add a new column to a table that your database already has. If a release needs one, taproot refuses to start and names the table and the missing columns, and that release's notes give the statement to run.

## A renamed key stops startup

Taproot reads `taproot.yaml` strictly. A key that taproot does not know stops startup with an error that names the key and its line. When a release renames or removes a key, the old key stops startup until you change it as the release notes say. Taproot never ignores a key silently.

`config.yaml` belongs to CLIProxyAPI, which ignores keys that it does not know.

## Roll back within one minor version

A rollback runs an older binary on data that a newer one wrote. That is safe only within one minor version, such as from `0.3.2` back to `0.3.0`. If a newer patch release added a `taproot.yaml` key that you set, remove the key before you roll back, because the older binary refuses keys that it does not know.

Rolling back across minor versions, such as from `0.3.0` to `0.2.4`, is not safe on the same data. To keep that option, do these steps before every minor upgrade:

1. Stop taproot.
2. Copy the whole data directory.
3. Upgrade and start taproot.

To roll back across minor versions:

1. Stop taproot.
2. Replace the data directory with the copy. Keep the current `auths/` directory instead of the one in the copy, because taproot refreshes OAuth tokens while it runs and the copied tokens may no longer work.
3. Start the older version.

The usage history, quota history, and request logs recorded after the copy are lost.

## Data from v0.1.0 is not carried forward

Releases after `taproot-v0.1.0` do not support its database, request logs, or backups, and no release converts them. To move from v0.1.0 to a later release:

1. Stop v0.1.0, so that two servers never refresh the same OAuth tokens.
2. Create a new, empty data directory.
3. Copy `config.yaml`, `taproot.yaml`, and the `auths/` directory into it. Change the `taproot.yaml` keys that the release notes list.
4. Start the new release on the new directory.

The usage history, quota history, request logs, and backups of v0.1.0 stay in the old directory.
