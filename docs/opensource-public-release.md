# Public source release profile

This repository contains a private development history and is not itself a public release target. Use the `web-api-public` source export profile to create a fresh release candidate. The profile exports committed Git blobs only, excludes Git history, and rejects files that contain private deployment paths, internal addresses, private repository references, or retired runtime sidecar references.

The release candidate must pass all of these checks before publication:

- source export verification with `--require-asset-rights` and `--require-license-texts`;
- zero private-content markers in the archive;
- complete third-party license and notice material;
- complete current web asset rights evidence;
- a fresh repository with a new root commit and no source-history carryover.

The current local candidate is regenerated from the cleanup branch during release review. It excludes private operational or historical references, including internal agent instructions and secret-scanning configuration. All included web assets must have verified rights evidence, and all dependency license records must have bundled evidence.

Dependency audit status for this candidate is recorded separately during release review. The PI runtime has no reported advisories after the `undici` refresh; the web workspace still has two low-severity SvelteKit advisories whose available fix is a major toolchain upgrade and therefore requires a dedicated compatibility review.

Publication remains an explicit owner action. This profile does not change repository visibility, push to a remote, or grant redistribution approval.
