# Architecture

Two binaries, one canonical event type (`internal/ingest.CanonicalEvent`).

## Ingest (`cmd/server`)

1. Cloudflare Worker (`edge/`) forwards method, path, headers, and body to Cloud Run.
2. Handler authenticates `Authorization: Bearer`, caps body and batch size, validates each event, then canonicalizes.
3. Canonicalize assigns a UUID `event_id`, UTC timestamps, `path`/`search` from `url`, and `ip_hash = sha256(ip + salt)`. Raw IP is dropped.
4. Publisher is either Google Pub/Sub (production) or a log sink (`PUBLISHER=log`).
5. Pub/Sub JSON encoding matches the Avro topic schema in `deploy/event.avsc` (all fields strings; JSON blobs stored as text).
6. BigQuery subscription writes `events_raw.events`. Views in `deploy/elt_*.sql` dedup by `event_id` and build a click funnel.

```
Worker → POST /v1/events → validate → canonicalize → Pub/Sub → BigQuery raw → views
```

The HTTP path is fail-closed on validation (whole batch rejected). Publish ACK is asynchronous and bounded; a delivery error is counted, not turned into `5xx`.

## Traffic-runtime (`cmd/traffic-runtime`)

```
GET /  → capture current release → Resolve(context) → 302 Location
                 ↘ enqueue canonical events (bounded queue)
```

- **Load path:** directory LKG and/or generation-pinned GCS objects (program, weights, release). Optional producer pointer object selects the pin set. Aliases like `latest` are not trusted.
- **Request path:** pure `internal/decision` over the captured release. Query-v1 allowlists `ta_source`, `ta_sub`, `campaign_id`, `zone_id`, `cost`. Unknown keys are dropped. Country is an **event** column after edge-token + ISO pin, never matcher context.
- **Telemetry:** `internal/runtimepub` queue (size/workers capped, publish timeout, drop counter). Distinct from ingest’s publisher.

`/health` is liveness. `/ready` is true only with an executable redirect release. `/contract` dumps the loaded activation (no secrets).

## Why two publishers

Ingest used a per-message ACK goroutine (now a semaphore + timeout). Runtime needs backpressure on the redirect hot path, so it uses a fixed worker pool and `TryEnqueue`. Both marshal the same canonical JSON so BigQuery schema stays one table.
