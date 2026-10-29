# Public release scope

This repository's public source release is the sanitized `web-api-public`
profile. It contains the Orrery Web/API source, self-developed runtime
utilities, dependency license evidence, third-party notices and manual release
documentation.

The public repository is created from a fresh single-commit export. The private
staging repository and its Git history are not published. Desktop/client
products, private operational material, the Claude Agent SDK sidecar, PageIndex
service source and sample assets, credentials and local build caches are outside
the public release.

Orrery-owned source is licensed under [Apache-2.0](LICENSE). Preserve
[NOTICE](NOTICE), [THIRD_PARTY.md](THIRD_PARTY.md), dependency license texts and
the attribution for Minvia and other copyright holders when redistributing.

Validation and release evidence are recorded under `compliance/`. Builds and
deployments are manual; GitHub Actions are intentionally disabled.
