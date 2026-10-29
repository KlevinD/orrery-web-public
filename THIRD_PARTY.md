# Third-party components

The root Apache-2.0 license covers Orrery-owned source. It does not replace
licenses in dependencies, imported source, fonts, images, or external services.
Keep existing copyright headers and license files when redistributing source.

| Component | Integration and evidence |
| --- | --- |
| Orrery AgentKernel / Core | Shared repository-maintained Go source in `orrery_v2/cmd/desktop-agent`; `cmd/desktop-core` remains compatibility source. Apache-2.0. Including shared Go source does not distribute a desktop application or executable. Historically adapted portions retain their source attribution below. |
| AionCore session source adaptations | Historical Go Session Runtime adaptations and a retained Claude fixture, sourced from `iOfficeAI/AionCore` commit `76f5554286ba0b6d33fb74d5c2bb2b3b0b83100d`, `crates/aionui-session`. Apache-2.0, Copyright 2025 AionUi (aionui.com). Preserve [the original license](compliance/licenses/aionui-session-76f5554-Apache-2.0.txt), NOTICE and [source provenance](orrery_v2/docs/runtime/runtime-source-provenance.md). No AionCore executable, Rust crate, download or checkout is required to build/run/test Web. This is retained source attribution, not an external runtime dependency. |
| Claude Code | User-installed CLI behind a Go native stdio adapter. No Agent SDK sidecar or Claude executable is bundled. CLI use follows the vendor's terms. The separate `@anthropic-ai/sdk` API client retains its own license. |
| PageIndex connection | Orrery supports HTTP connections to a compatible independently operated PageIndex knowledge service. Project: [VectifyAI/PageIndex](https://github.com/VectifyAI/PageIndex). The Orrery Web/API release contains the connector and its configuration, with the independent service, Python dependencies and images outside this release. See [the connection scope](docs/third-party-integrations.md); compatibility follows the Orrery service contract. |
| Local knowledge storage | Orrery-owned JSON chunk storage and cosine search in `internal/knowledge/vector/jsonstore`. The unused LanceDB/Arrow Go experiment and its dependencies have been removed. The persisted `lancedb` backend identifier and directory name remain compatible with existing JSON data; they do not load a LanceDB engine. |
| PI coding agent | Bundled `@earendil-works/pi-coding-agent` v1.3.0, [MIT](https://github.com/earendil-works/pi/blob/v1.3.0/LICENSE), Copyright 2025 Mario Zechner. Its original license is included in `compliance/licenses/pi-coding-agent-1.3.0-MIT.txt`; PI's transitive packages retain their own terms. The Orrery wrapper's Apache-2.0 does not replace PI's MIT notices. |
| SILK audio decoder | `silk-wasm` 3.7.1 decodes SILK voice messages to PCM in `orrery_v2/scripts/audio-codec`. Its npm package is MIT, Copyright 2024 idranme; underlying SILK source retains its own original notices. The audio lock graph and original/supplemental notices are included in `compliance/license-texts.json`. |
| Feishu/Lark Go SDK | Vendored replacement of `github.com/larksuite/oapi-sdk-go/v3` v3.9.9. MIT, Copyright 2020 Lark Technologies Pte. Ltd.; preserve [the original license](orrery_v2/third_party/lark-sdk/LICENSE) and source attribution. |
| DingTalk Stream Go SDK | Vendored replacement of `github.com/open-dingtalk/dingtalk-stream-sdk-go` v0.9.1. MIT, Copyright 2023 钉钉开放平台团队; preserve [the original license](orrery_v2/third_party/dingtalk-stream-sdk/LICENSE) and source attribution. |
| Ory Kratos / Ory Hydra / OpenFGA | External identity/authorization services configured by the identity Compose overlay. Their project source uses Apache-2.0; selected image versions/digests and their dependency notices require release-specific records. See [the integration review](docs/third-party-integrations.md) for primary license links. |
| PostgreSQL / container bases | Separately pulled images. Record immutable image digests and their own license notices for each release. Base OS/Node/Python packages retain separate licenses. |
| Centrifuge | Vendored in-memory distribution of `github.com/centrifugal/centrifuge` v0.38.0, MIT, Copyright (c) 2023 Centrifugal Labs LTD. Redis adapters are excluded, removing the Redis client dependency. Preserve [the original license](orrery_v2/third_party/centrifuge/LICENSE), the internal WebSocket license and [adaptation record](orrery_v2/third_party/centrifuge/ORRERY_ADAPTATION.md). |
| Go modules | Pinned by `orrery_v2/go.mod` and `go.sum`; Web/API source inventory and frozen license-file evidence in `compliance/web-api-source-inventory.json`. |
| SaaS Web / PI runtime | Pinned npm graphs in `compliance/web-api-source-inventory.json`. Unused ELK and aggregate D3 layouts and bits-ui are removed from the Web candidate; the active force-graph/d3-force chain retains its original licenses. PI is separate from the removed Claude Agent SDK runtime. |
| Archify architecture documents | The three generated architecture viewers embed Archify 3.1.0 runtime/templates under Apache-2.0. The upstream [LICENSE](orrery_v2/docs/architecture/panorama-2026-10-15/ARCHIFY-LICENSE.txt), [NOTICE](orrery_v2/docs/architecture/panorama-2026-10-15/ARCHIFY-NOTICE.txt) and [third-party notices](orrery_v2/docs/architecture/panorama-2026-10-15/ARCHIFY-THIRD_PARTY_NOTICES.md) are retained beside the generated files. Archify is used only to render authored Orrery diagram data; its names and logos do not imply endorsement. |
| Additional embedded source notices | Original source comments in PI, SvelteKit, Node types, es-module-lexer, highlight.js, Playwright, pngjs, source-map-js, tough-cookie, Vitest and yargs retain their independent author notices. Each reviewed comment is preserved without rewriting, with the locked archive, member digest and exact byte range in `compliance/license-text-overrides.json`. Package-level licenses remain separate; abbreviated publisher comments are not presented as complete replacement license texts. |
| Playwright test tooling | Apache-2.0. The 1.61.1 lock metadata is verified against the integrity-checked npm package; its original LICENSE and NOTICE are preserved under `compliance/licenses` and hashed in `compliance/npm-license-evidence.json`. Browser executables are not included in the source export. |
| DM Sans / JetBrains Mono Web fonts | Ten unmodified WOFF2 files match Google's official distribution byte for byte. Retain their original copyright and full [DM Sans](orrery_v2/web/static/fonts/OFL-DM-Sans.txt) / [JetBrains Mono](orrery_v2/web/static/fonts/OFL-JetBrains-Mono.txt) OFL-1.1 texts. This license applies to the fonts. Fixed file/source/license hashes are in [the Web asset review](compliance/web-asset-rights.json). |

Dependencies can have non-permissive or delayed-conversion licenses, including
build tooling and native libraries. Do not label the entire graph Apache-2.0.
Binary distributions must carry required notices, license texts and
corresponding source as required by each selected license.
The SBOMs are source inventories. Complete collected dependency notices are
recorded separately in `compliance/license-texts.json`; the current Web/API
lock graph has zero publisher-reference-only components. Four historical gaps
remain documented for the replaced upstream packages, outside that graph. The external PageIndex image and its
sample PDFs are outside this source distribution.

`compliance/assets.json` records hashes and repository provenance. Provenance
does not establish authorship or a redistribution grant. Missing original
license evidence is explicitly marked for review before public distribution.
The [Web asset review](docs/opensource-asset-rights.md) verifies ten font files and
six Lark SDK documentation images. Twenty-four older images have been removed from the current source export; their unresolved historical provenance records remain. Source correspondence does not grant trademark endorsement.
The five talent-market profiles are synthetic; default portraits are now generated by Orrery source.

Original Go/npm dependency notices, verified against their pinned module/npm archives,
are preserved byte for byte under `compliance/licenses/collected/` with component and
source identities in `compliance/license-texts.json`. This collection includes build,
test and optional platform packages; it is a source superset rather than a claim
that every package ships in a running binary. Use
`python scripts/third-party-license-bundle.py check` and the manual release procedure
to verify and package the texts with LICENSE, NOTICE, source attributions and font
licenses. Component-specific source/linking and resource conditions still apply.

The collector also preserves publisher notice variants such as `ThirdPartyNotices.txt`,
`ThirdPartyNoticeText.txt`, `CopyrightNotice.txt`, `MIT.LICENSE` and `*.js.LICENSE`,
including Go REUSE sidecar `.license` files and nested npm bundle notices.
Text files inside `LICENSES/` and `LICENCES/` retain SPDX-named license texts
and separately attributed embedded SDK licenses.
Reviewed comments embedded in source are checked against their original locked
archive bytes during collection; source headers are not classified automatically.

The former `is-reference`, `locate-character`, `stackback` and
`proxy-agent-negotiate` implementations have been replaced with independently
implemented Apache-2.0 Orrery packages. Compatibility import names remain aliases;
Web/PI npm locks resolve them to repository source in this scoped candidate.
Svelte's CommonJS entry and embedded helpers in the pinned Web Rollup
bundles also use the replacements. Each Orrery package includes LICENSE and NOTICE;
`compliance/orrery-js-sources.json` pins its complete packaged source and license
identity. Unknown local dependencies are rejected, not exempted from collection.
The current Web/API notice graph has zero publisher-reference-only components.
This does not resolve the original publishers' missing material or approve public
release. See [implementation and validation limits](orrery_v2/runtime/orrery-js/README.md).

The legacy Claude Agent SDK sidecar is excluded from this source candidate. The
default API image and Claude adapter use the user-installed CLI/native adapter;
no Agent SDK package, sidecar entry point, sidecar test, or sidecar lock graph is
redistributed here. The former private synchronization review remains historical
evidence only.

Publisher project originals are preserved for `bezier-js@6.1.4` (a later commit
with the identical version/package manifest) and `@tybys/wasm-util@0.10.1`
(explicitly a later same-project 0.10.3 license). The removed `uri-js-replace`
archive and its conflicting later MIT-0 project evidence are no longer part of
the active lock graph. See [evidence scope](docs/third-party-license-bundle.md).

Web / Go dependency removal (2026-10-16): Multica-derived storage and mention code is replaced. Eino model/message interfaces are replaced by `internal/agent/llm` and `providers/profilemodel`; Eino and its schema module are absent from Go dependencies. Redis server deployment and the optional Redis client backend are absent. AionCore remains historical source attribution only; no external installation is required. PR269 deployed this dependency refactor for the API and SaaS Web on 2026-10-19 at production commit `81df3a7ecc31b748e1983e6085609dfbe3aa6b20`; independent services and desktop packages have their own release records.
The optional Web Tauri JavaScript bridge (`@tauri-apps/api@2.11.0`) and CLI
(`@tauri-apps/cli@2.11.2`) retain their Apache-2.0/MIT original texts under
`compliance/licenses/tauri-*`, with archive integrity in
`compliance/npm-license-evidence.json`. The separately retained SILK MIT,
SDK BSD and Skype notices remain in `compliance/licenses/silk-wasm-*`.
Desktop/Tauri Rust source and native binaries are outside this export.
