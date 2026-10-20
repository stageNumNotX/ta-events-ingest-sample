# Agent / contributor notes

Personal Go sample: **ingest** (`cmd/server`) plus **traffic-runtime** (`cmd/traffic-runtime`). Work in this repository only.

## Do first

1. Read [README.md](README.md) and [docs/architecture.md](docs/architecture.md).
2. `make test` and `go vet ./...` before claiming done.
3. Keep ingest HTTP/auth backward compatible. Additive fields on `CanonicalEvent` are fine; breaking `POST /v1/events` is not.

## Boundaries

- Resolver is a pure Go function over an immutable in-memory release. Redirect execution is a separate module in the same process. No Worker-side resolver, no network decision API.
- One request captures the current release. GCS is load-path only.
- Do not invent a country/URL registry or a competing traffic-pointer schema. Producer artifacts are read-only inputs.
- Enqueue is not a confirmed publish. Keep queues, retries, and background work bounded. Count ACK / errors / drops separately.
- Never commit credentials, tokens, raw IP, Authorization/cookies, or full env dumps. Do not print secret values. Use env / Secret Manager / WIF.
- Do not force-push `main`. Do not expand WIF to every branch.

## Ingest rules

- Extract+load only. Bot flags, dedup, and funnels belong in BigQuery SQL.
- `cost` is a string event field, not matcher context.
- Campaign/zone are event attributes, not Location / matcher keys.

## Runtime rules

- Event `country` only after `X-Traffic-Edge-Token` matches `TRAFFIC_EDGE_TOKEN` (constant-time) and the value is in the pinned ISO alpha-2 set. Do not copy country into `decision.Context`.
- Packed `ta_sub` (`digits_digits`) is a fallback for one affiliate source when explicit `campaign_id` / `zone_id` are empty.

## Tests

```bash
go test ./...
go test -race ./...
python3 scripts/traffic-runtime-events-eval.py --self-test
python3 scripts/traffic-runtime-t11-eval.py --self-test
```

Mock Pub/Sub in ingest tests (`internal/ingest` `Publisher` interface). Do not hit live GCP from unit tests.

## Docs language

Public docs are English. Do not add client names, personal emails, or live project IDs.
