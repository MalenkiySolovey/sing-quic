# sing-quic source and patch audit

SOURCE_AUTHORITY=https://github.com/SagerNet/sing-quic

COPY_ROLE=mirror/cache/development

Copy: https://github.com/MalenkiySolovey/sing-quic. Upstream authorship, full Git ancestry, licenses and notices are retained. This is a source copy with a temporary patch queue, not an independent core product. The Panel owns selection, adapters, capabilities and exact build pins; this repository owns engine changes.

Selected upstream: 75c3ac4fa12b4ded2dc01b392ed7d5eaf3ea6876, 75c3ac4fa12b4ded2dc01b392ed7d5eaf3ea6876, tree cd7ca728a96a2a5c73df5525130996dbda52e0ee. Pristine ref: mirror/extended-75c3ac4. Patch branch: solovey/extended-patches. It contains 2 ENGINE_PATCH_REQUIRED logical source commits and one repository policy/CI commit. The latter changes documentation/CI only. No upstream merge commits belong in this queue.

The pre-policy code tree f65f7e4f657707a2ff480d80f86558e0634a5fe5 is byte-identical to every source and regression-test file in historical d0ecb33c441a8f188f98bbcf07c0e1d77785e7be. Intentional removals are SOLOVEY_INTEGRATION.md; their provenance and CI duties move here. No compiled behavior is removed.

Official separately retains its accepted 6a3a24d65b99587fad1d4cdd567c88f212acdd63 → b2eea8ca8762dd1e438171e2f7b56f9715c30cbb parent-control integration. Extended uses 75c3ac4fa12b4ded2dc01b392ed7d5eaf3ea6876 plus the two ordered fixes here. Both have SagerNet/sing-quic authority; they are different upstream generations, not separate providers. The upstream 75c3ac4 base already contains the TUIC authentication mutex; it is not duplicated. Parent control cannot be implemented by guessing child sockets in Panel, and early-data read/close ownership belongs to this transport.

All local code changes are upstream contribution candidates, prepared but not submitted. Removal requires equivalent upstream behavior plus the same passing regressions. External upstream PR submission needs operator authorization.

Pristine branches must receive only fast-forward synchronization to reviewed upstream points; patch/topic PRs target solovey/* branches. Branch protection should reject ordinary integration PRs into mirror/* while allowing the synchronization identity to fast-forward. No upstream or pristine ref is rewritten by this migration.
