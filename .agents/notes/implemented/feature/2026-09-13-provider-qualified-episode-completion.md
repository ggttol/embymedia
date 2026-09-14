# Agent Note: Provider-qualified automatic episode completion

Status: implemented

## Problem

Automatic episode completion could inspect and selectively save only 115 shares. The Quark-to-115 importer could move a complete saved share only to `/emby/_待整理`; connecting it directly would copy unrelated files and permit destination drift across restart.

## Decision

Automatic completion searches 115 and Quark separately, inspects each live share tree, and preserves the existing title, year, TMDB, season, and episode evidence rules. Preview performs no provider writes. More than one usable resource for an episode is a visible conflict; an operator must select a provider-qualified resource before transfer.

115 leaves continue through selective share receive. Quark leaves enqueue a durable child import containing only selected source IDs and expected episode labels. Before any Quark save, the child revalidates the configured Quark target, current `c115_cid_map`, fixed 115 account, Emby library, canonical Series path and TMDB identity, and exact direct-child 115 folder. That binding and all transfer checkpoints are immutable across restart and retry. Public task requests cannot submit these internal binding fields.

Quark share responses do not consistently include `revision`. When absent, the adapter derives a deterministic non-secret revision from the source ID, name, size, and object type; the child requires the same fingerprint from a fresh share snapshot before saving. A transient empty 115 directory inventory is retried for up to 29 seconds, while duplicate matching folders still fail immediately. If Quark rejects a browser-identity large-file download with its size-limit response, signed-URL acquisition retries once with the official desktop-client identity and preserves that identity for the CDN request. Downloads split the spool into 32 MiB segments fetched over parallel ranged connections, because a single Quark stream measured about 1.4 MiB/s while four connections measured about 4.9 MiB/s; concurrency is the validated `quark_download_connections` setting (default 4, maximum 8, and eight measured lower than four). Each segment is fsynced and checkpointed before the next one is claimed, segment checkpoints and the spool path are keyed by the source object so a retry reuses the bytes an earlier attempt downloaded instead of fetching them again, and the full-file SHA-1 is re-verified against the Quark identity before upload. A segment request carries its own timeout so a silent CDN connection fails and reopens rather than pinning the single transfer worker. A segment that dies mid-stream reopens its range from the bytes already written, up to four attempts, because the CDN emits transient HTTP/2 stream errors. Destination verification treats a missing same-name object as still being published and waits, extending while 115 still exposes CloudDrive2's `<name>**..uploading` placeholder, so a large upload backlog can drain without failing; it gives up after one full idle stretch with no observable progress (a floor of two minutes plus one second per MiB, capped at thirty minutes) or four hours overall, whichever comes first. A present object that differs in folder type, parent, name, size or SHA-1 belongs to a different file and fails immediately, and a conflicting object is never overwritten. The upload step uses the digest computed from the spool rather than the pre-download row, and an import without a known spool digest fails before contacting the provider. Cookie-only 115 fallback publishes flat into the fixed `/emby/_待整理` staging directory, because the CloudDrive2 FUSE mount reports new subdirectories as root-owned without inheriting ACLs; `ensure-transfer-access.sh` grants the service user access on the staging and 追更 trees from the service unit and from the minute CloudDrive monitor.

A bound child uploads files directly into the verified Series folder, verifies each 115 object, synchronizes STRM, scans Emby, revalidates canonical identity, and fails if any selected episode remains missing. The parent completion task reports the child as queued rather than claiming transfer success.

## Alternatives considered

Provider-priority first match was rejected because it silently hides multiple plausible resources. Copying the whole Quark share to `_待整理` was rejected because it transfers unrelated episodes and loses the Series destination guarantee. Performing Quark transfer inline inside the parent was rejected because the single worker could not offer an independently recoverable checkpoint lifecycle.

## Consequences

Quark automatic transfer requires a default Quark account and `quark_autofill_target_id`. Selected Quark files remain in Quark. Operators see candidate evidence, conflicts, queued child IDs, byte/file phases, cancellation, and checkpoint-preserving retry. A completed parent with a queued child is not final episode completion; the child owns provider, STRM, and Emby verification.
