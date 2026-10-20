-- Local additive template for raw cost STRING (lane B / Mac applies live).
-- CREATE TABLE IF NOT EXISTS / schema-exists is NOT a migration.
-- This file is not applied by ingest deploy.yml or this implementation PR.
-- Run AFTER inventory; record the bq job id. Keep Pub/Sub schema revision
-- ids for backlog/rollback (gcloud pubsub schemas commit is separate).
--
--   bq query --use_legacy_sql=false --location=EU < deploy/migrations/20260920_event_cost.sql

ALTER TABLE `YOUR_GCP_PROJECT.events_raw.events`
  ADD COLUMN IF NOT EXISTS cost STRING;
