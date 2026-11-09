# Patched crates

To build taproot with a modified copy of one of the codec's crates, such as an LGPL crate under the terms of its license, put the modified crate in a directory here, for example `patches/cabac`. Then point a `[patch.crates-io]` entry in `../Cargo.toml` at it and run `cargo update -p <crate>`. The "GNU LGPL" section of [THIRD_PARTY_NOTICES.md](../../../../../../THIRD_PARTY_NOTICES.md) has the full steps.

`Containerfile.taproot` copies this directory into its codec build stage, so the image build uses the patch too.
