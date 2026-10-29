# Web/API source synchronization — 2026-10-26

> Historical synchronization receipt. The four utility implementations and their evidence have since been updated by [the private self-developed replacement sync](opensource-self-developed-sync-20261026.md). Results below describe the earlier selected snapshot.

This candidate updates open-source preparation PR #1 from public input
`1990cdf7725588dce9e0752cfb40e842f3dba1d6` to the committed Web/API and shared
source selected from private source commit
`0ae5691f0c8cd8f05c0748c9824bc8bf0c662fe9`. The source is fixed at that commit;
uncommitted development changes are outside this snapshot.

## Source and attribution

The `orrery-web-source-v1` export selected 6,041 source files and excluded
2,344 files. It includes the current AgentCore/AgentKernel runtime, task
permissions, scheduling, knowledge/ontology services, API contracts and Web
changes. Client applications, native executables, private configuration,
sample PDFs and GitHub Actions workflows remain outside this export.

The previous candidate's Vite 6.4.5 security update, Redocly AJV 8.18.3 override,
SILK originals and optional Tauri JavaScript/CLI original license texts are
retained. Historical AionCore/PI attribution is retained. Three adapted Go
file digests were refreshed to their current local bytes without changing
their upstream provenance. Five imported documentation files have only their
extra final blank lines removed. Two archived generated documents have trailing
whitespace normalized, preserving Markdown hard breaks as explicit backslashes.

The source-selection archive SHA256 is
`019aaf28d86890ab5bc62cfd9745fcc9afebe87fd71759ffb02d7e721efe0b14`.
It identifies the private-source selection before public metadata overlays;
the final candidate and notice archive have separate hashes in the local
validation receipt.

## License status

[The complete notice manifest](../compliance/license-texts.json) covers
591 distinct components: 97 Go and 494 npm components, including build, test
and optional-platform dependencies. Vite's evidence was regenerated from its
integrity-verified 6.4.5 npm archive, and lock hashes and source inventories
were regenerated for this candidate. The 16 retained asset records pass the
export rights check. Go toolchain, vendored and historical source notices
are included in the separate notice bundle.

Four components still have **publisher-reference-only** evidence:

| Component | Version | Remaining gap |
| --- | --- | --- |
| is-reference | 3.0.3 | Complete upstream original license text |
| locate-character | 3.0.0 | Complete upstream original license text |
| proxy-agent-negotiate | 1.1.0 | Complete upstream original license text |
| stackback | 0.0.2 | Complete upstream original license text |

Their publisher MIT declarations and explicitly identified reference texts
remain recorded. The manifest retains `redistributionApproved: false`. Passing the manifest
checker verifies coverage, hashes and explicit gap records; it does not
close these four gaps or approve redistribution.

## Manual validation

- Web dependency installation, API generation, Svelte/type checks and production
  build passed. API generation reproduces the selected source contract.
- Web Vitest: 1,347 passed, 1 skipped, 0 failed.
- Source-export tests: 13 passed; notice-bundle tests: 27 passed; Web/API
  deployment configuration tests: 3 passed.
- Web backend-dependency boundary check passed: 831 server packages,
  no retired dependencies.
- Full Go Linux cross-compilation passed for all 179 packages with tests.
  Linux execution completed for all 179 packages: 7,878 top-level cases
  passed, 88 skipped and 1 failed. The initial server package exceeded the
  harness's 300-second overall limit without an assertion failure; all of its
  1,477 cases were rerun in four exhaustive, disjoint shards
  with two concurrent workers and a 600-second limit per shard.
  Server shard result: failure; see receipt.
  [The validation receipt](../compliance/web-api-validation-20261026.json)
  preserves the initial timeout, each package result and shard coverage.
- `git diff --check`, source inventory checks and the 591-component notice
  manifest check are part of the final candidate verification.
- Gitleaks raw exit code was 1 for 40 findings. Every finding matched the exact
  normalized bytes of a previously reviewed non-secret example, test fixture,
  source identifier or executable digest; no unmatched finding remained.
  The scan used an empty ignore file and ignored inline allow comments.
  [The current review](../compliance/source-export-secret-review-20261026.json)
  records classifications and hashes. Git history was not scanned.

The Linux tests use the exported source snapshot and Linux binaries compiled
with Go 1.25.0. Isolated fixture path mappings accommodate Windows source paths
embedded by cross-compilation; fixture builds use a real Linux Go toolchain.
Test skips and failures are reported separately from compilation success.

## Open validation and release items

Backend source-quality validation currently fails
`TestProductionServerFilesStayFocused`: `orrery_v2/cmd/server/main.go` has
803 lines against the existing 800-line maximum. These are the selected
private-source bytes. The test and its threshold are retained; the sync
does not claim a passing backend gate.

The refreshed [official npm audit evidence](../compliance/dependency-audit.json)
reports these vulnerable-package counts for the unchanged selected lock graphs:

| Lock graph | Low | Moderate | High | Critical |
| --- | ---: | ---: | ---: | ---: |
| Web | 2 | 1 | 0 | 0 |
| PI runtime | 0 | 1 | 1 | 0 |
| Audio codec | 0 | 0 | 0 | 0 |
| Claude sidecar | 0 | 3 | 5 | 1 |

These counts include transitive and development dependencies. They are audit
findings, without a claim that every advisory is reachable in production.
The older October 19 zero-finding audit is historical evidence and is
superseded by this candidate's audit. Dependency remediation and its associated
compatibility and license checks remain release work.

Design baseline revision 8 contains 15 articles and 29 contracts. Its monorepo
verifier reports 10 missing links/references to the excluded desktop product
under `ORR-UI-01`; it does not pass on this scoped export. The shared baseline
and its contracts were retained without relaxing the verifier. This source
sync does not claim production, business-delivery or desktop acceptance.

GitNexus has no index for this public worktree. Its other indexes point at older
private-source commits, and `detect_changes` rejects this worktree as unindexed.
Source selection, Git diffs and actual checks provide this review's evidence.

This change updates a review branch and PR. Repository visibility, merging,
release publication and production deployment are separate actions. GitHub
Actions and automatic deployment remain disabled.
