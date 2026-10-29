# RSI production shadow release

This release adds bound tool-failure observations, complete local evidence persistence, and Laya shadow classification to the exact deployed `opensource-pruning-20261015T041709Z-48ed822815` source snapshot. The base source archive SHA-256 is `3e0bc6224397fc0da9fbe45c8e9dfd309fc0cb08bd789f249b5960b7d15080e0`; the base executable SHA-256 is `aa629d0861cf6c9891ef3fe09d5b4e717e0ae14421763b9627f2bac5c239693e`. The new production knowledge JSON store and all current responsibility, scheduler, website and WeChat changes are preserved.

The existing core database stores immutable observations and the latest classification attempts, scoped by tenant and agent. Laya receives bounded excerpts through its loopback API. Raw tool evidence stays in the core database and is omitted from logs and model input. Queue, inference or persistence failures retain the original business result; persistence failures are logged without raw evidence. Host administrators can list, inspect or retry through `resourcectl rsi-samples`.

Production uses `ORRERY_LAYA_MODE=shadow`. Existing authorized knowledge observations preserve the original results and ordering. Startup keeps the current handler options, initialization and worker behavior. The runtime tooling constructor lives in a separate source file to retain the existing source-size limit.

This release contains the observation and classification stage of RSI. The newer Skill content-snapshot and atomic-promotion implementation is not included. Real candidate generation, independent baseline/candidate experiments, automatic Skill activation, optimizer self-improvement, durable inference retries and sample retention policies remain future work. No production token savings have been measured.

Deployment verifies the source and executable hashes, captures native test and real-model canary receipts, preserves the previous release and frontend, uses the shared deployment lock, and verifies effective shadow mode and API readiness after restart. Startup failure restores the previous release. Full-repository release gates are not claimed to have passed.
