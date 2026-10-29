# Open-source compliance readiness (2026-10-27)

This review covers the Web/API source candidate at the readiness commit recorded
in `compliance/open-source-readiness-20261027.json`. It is a private readiness record;
it does not change repository visibility or publish the candidate.

The source-export command records the candidate commit, member hashes and final
archive SHA-256 in `.source-release/manifest.json` and the external artifact
record. The archive contains committed files only and no Git history.

The current private candidate is generated from the `web-api-public` profile.
Its manifest and external artifact record carry the exact source commit, file
counts and SHA-256 for each generated archive.

The repository-side compliance checks are closed for the current candidate:

- 592 dependency records have complete collected license evidence and the current
  Web/API graph has zero publisher-reference-only components.
- All 16 current Web/API assets have verified rights evidence. Retired assets with
  unresolved provenance are absent from the candidate bytes.
- Archify's LICENSE, NOTICE and third-party notices travel with the generated
  architecture viewers.
- The Claude Agent SDK sidecar and its lock graph are absent from the candidate.
- PageIndex service source, images, dependencies and sample PDFs are outside the
  candidate; Orrery includes only the connector and compatibility documentation.
- The source export passed asset-rights and license-text verification.

The company owner has confirmed in this review session that the publisher is
authorized to redistribute the Minvia-attributed Orrery source and modifications.
The Minvia attribution therefore remains in place. The session statement is
recorded as an attestation, while a signed or otherwise auditable company record
is still preferable for the final release file.

Several publication blockers remain even after that rights attestation. The
current Git history contains the removed Claude Agent SDK sidecar in earlier
commits, so changing this repository to public in place would expose that old
tree. The current tree also contains private repository links, production paths,
OpenClaw paths and an internal address in operational files and historical
records. The public release must be a fresh repository from the verified tree or
use a separately reviewed history rewrite, and it must use a sanitized public
profile.

The refreshed npm audit reports Web 2 low-severity findings, with no moderate or
high findings; PI and audio have zero findings. The Web fixes require the
SvelteKit/adapter-static major toolchain upgrade. This is a security release-review
item, not a license gap; complete the upgrade and compatibility checks or record
an explicit owner risk acceptance before publishing a supported release.

An isolated upgrade evaluation cleared the Web audit by moving to SvelteKit 3.1.0,
adapter-static 4.0.0, Vite 8.3.5, the Svelte Vite plugin 7.3.1, TypeScript 6.0.3
and Svelte 5.57.3. The candidate then failed Orrery's utility precheck because
`orrery_v2/runtime/orrery-js/install.mjs` deliberately verifies the self-developed
Svelte 5.56.0 compiler bridge. This requires a separate compiler-bridge and
license-evidence migration; the major toolchain change is not included in this
release-preparation commit.

The existing `redistributionApproved: false` fields remain until the sanitized
public profile, history decision and dependency security review are recorded in a
new immutable release decision. They are approval records, not missing license
text.

The four historical npm packages with unresolved original license evidence are
outside the current dependency graph after the self-developed replacements. Their
records remain in [the historical review](../compliance/license-upstream-review-20261026.json)
and must not be presented as a grant for those old packages.

The machine-readable status is in
[`compliance/open-source-readiness-20261027.json`](../compliance/open-source-readiness-20261027.json).
The candidate must remain private until the two release blockers have an
attributable approval record; changing a boolean alone is not a compliance fix.
