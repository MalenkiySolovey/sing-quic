# quic accepted source copy baseline

Authority: https://github.com/SagerNet/sing-quic. Copy: https://github.com/MalenkiySolovey/sing-quic, role mirror/cache/development. Preserve original history, authorship, license and notices. This is a dependency source copy, not a third core provider.

Selected base: 6a3a24d65b99587fad1d4cdd567c88f212acdd63, tree 4a71776d8bf93353aff895137c852a6cbf127b7f. Pristine ref: mirror/official-6a3a24d. Canonical patch branch: solovey/official-patches. Exactly one previously accepted source commit b2eea8ca8762dd1e438171e2f7b56f9715c30cbb, tree 66be202475c03b17f6aa22c67df4653c7662d802, is retained unchanged. Repository documentation and CI commits change no compiled source. Panel module version/checksums remain unchanged.

Classification: ENGINE_PATCH_REQUIRED. Problem: Authenticated Hysteria/Hysteria2/TUIC parent retirement must close the original transport parent and synchronously retire child state. TUIC initial data must remain owned until its stream read/close completes.

Panel/provider adaptation is insufficient: Panel owns authenticated principal bindings and retirement requests. Private transport parents, parser buffers and concurrent close are library-owned and cannot be safely reconstructed from child sockets in a Panel adapter.

Affected upstream behavior: authenticated transport parent retirement and TUIC early-data lifetime.

Regression: ./... race tests on Linux and Windows, three repetitions. The library owns these tests; Panel separately validates runtime consumption.

Selected upstream base: 6a3a24d65b99587fad1d4cdd567c88f212acdd63. Contribution status: upstream submission candidate, prepared but not submitted. Removal condition: accept a reviewed authority base that provides equivalent behavior and passes the same regression without this patch.

The original accepted commit predates the new trailer discipline; its exact ID is explicitly grandfathered by the queue verifier so existing Panel build pins need no rewrite. Future source changes require complete problem/boundary/regression/base/contribution/removal trailers and one logical correction per commit. Generate patch files only for review, contribution, or disclosure. Canonical state is this linear Git queue.

The pristine ref accepts reviewed fast-forward upstream synchronization only. Normal development PRs target solovey patch/topic branches; never merge upstream repeatedly into the patch queue. No historical ref is deleted or rewritten.
