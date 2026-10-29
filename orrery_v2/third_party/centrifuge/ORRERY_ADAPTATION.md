# Centrifuge in-memory distribution

Source: github.com/centrifugal/centrifuge v0.38.0 (MIT).

Orrery retains the upstream in-memory broker, presence manager and transport.
The optional Redis broker, Redis presence manager, Redis shard, Redis Lua assets, examples and upstream tests are excluded. No Redis client or server is required by this distribution.
Upstream Go implementation files are otherwise unchanged; module requirements are pruned to the retained packages. LICENSE and internal WebSocket license are retained. Orrery tests cover its WebSocket integration.
