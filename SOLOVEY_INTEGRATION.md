# Solovey pinned parent lifecycle integration

This is a maintained integration delta against official SagerNet/sing-quic
`6a3a24d65b99587fad1d4cdd567c88f212acdd63`
(`v0.7.1-0.20260924092235-6a3a24d65b99`). It supports the official sing-box
v1.14.2 semantic baseline. It is not an unmodified official build.

The original GPL-3.0-or-later license and copyright notices remain intact.
Integration additions are Copyright (C) 2026 MalenkiySolovey and distributed
under GPL-3.0-or-later. Original S-UI v1.6.4 supplied a behavioral witness for
graceful close; its quicgrace implementation was not copied.

Only these implementation seams change:

- `parent.go`: constructor-scoped typed hook and one per-service owner of
  accepted QUIC handles, their authenticated user, admission and close lifetime.
- Hysteria, Hysteria2 and TUIC `service.go`: retain their original verifier,
  protocol handling, TLS, congestion and options; associate the existing verified
  user with the accepted handle and delegate transport lifecycle to that owner.
  Authentication publication and stream error variables are synchronized.
- TUIC `early_data.go`: retain the parser's bounded initial payload safely when
  network Read and Close run concurrently. This avoids the upstream CachedConn
  buffer lifetime race witnessed during the integration race tests.
- Hysteria rejects bad passwords without including the password in an error.

Authentication remains authoritative in each original protocol service. No
address lookup, DB identity, global registry, or replacement flow tracker is
introduced. IDs are monotonic within one owner and need the embedding runtime's
inbound epoch and generation to be usable as mutation targets.

Closing rejects new admission, cancels Accept, and waits for its result barrier.
Tracked parents call the pinned quic-go CloseWithError before acceptor shutdown.
The public listener Close stops handshakes while preserving established handles;
its finite completed queue is subsequently accepted and intentionally closed by
the same owner before the embedding inbound can close UDP. This ordering uses
barriers and a two-second deadline, with at most eight concurrent tracked close
workers. A deadline is an error, not a delivery assertion. Listener Close is
initiated exactly once with a bounded waiter. No sleep establishes correctness.
quic-go's emission attempt does not guarantee delivery across a broken network.

The official sing-box inbound closes UDP before calling service.Close. Solovey
uses a typed wrapper of each exported official inbound to drain this exposed
service owner first and preserve optional interfaces. No sing-box source patch,
private-field access, reflection, unsafe code or protocol service copy is used.

Tests in `parent_test.go` cover admission fencing, 24 live transport handles,
close-once, late Accept, completed queue, bounded listener wait and cleanup.
`tuic/early_data_test.go` covers payload order and concurrent buffer close.
Solovey's integration tests separately authenticate all three real protocols
and exercise lifecycle, selective termination, NAT, port migration, stale epoch
and generation, disable/re-enable, rejected TCP admission and routed UDP.

Run these tests under the Solovey module's selected dependency graph, including
`github.com/sagernet/quic-go v0.61.0-sing-box-mod.7` and Go 1.26.9:

    go test -race github.com/sagernet/sing-quic github.com/sagernet/sing-quic/tuic
    go test -race -tags=with_quic ./core/runtime ./core/inboundidentity ./core/tracker ./core/registry

The consuming repository records the exact replacement module commit and Go
checksums, a complete replayable patch against the baseline, its SHA256 and
explicit package build metadata. Updating either source pin requires reviewing
the integration seam and repeating the affected integration and race tests.
