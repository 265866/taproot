# Stability

Taproot is not at version 1.0 yet. This page says what a release can change and what stays compatible. [Update taproot](updating.md) shows how to upgrade, roll back, and move from 0.1.0.

## A minor release can break things

Releases are tagged `taproot-vX.Y.Z`. Before 1.0, the version numbers mean this:

- A minor release, such as `0.2.0` to `0.3.0`, can break things. It can rename or remove a `taproot.yaml` key, change the `/taproot/api/v1` API, or change how the dashboard works.
- A patch release, such as `0.2.0` to `0.2.1`, fixes bugs.

Each release's notes, in [CHANGELOG.md](../CHANGELOG.md) and on the GitHub release, say what changed and what you must do before you upgrade. Read them before every minor upgrade.

A rollback is safe on the same data only within one minor version. [Roll back](updating.md#roll-back) shows how to roll back across minor versions with a copy of the data directory.

## Database changes only add tables and columns

Taproot keeps usage, quota, and the request-log index in `taproot/taproot.db` in the data directory. A release changes that database only by adding tables, indexes, or columns. It never renames or drops a table or a column.

Taproot does not add a new column to a table that your database already has. If a release needs one, taproot refuses to start and names the table and the missing columns, and that release's notes give the statement to run.

## A renamed key stops startup

Taproot reads `taproot.yaml` strictly. A key that taproot does not know stops startup with an error that names the key and its line. When a release renames or removes a key, the old key stops startup until you change it as the release notes say. Taproot never ignores a key silently.

`config.yaml` belongs to CLIProxyAPI, which ignores keys that it does not know.
