-- Live additive migration for click attribution (lane B applies this).
-- CREATE TABLE IF NOT EXISTS / schema-exists is NOT a migration.
-- Run AFTER inventory; record the bq job id. Keep Pub/Sub schema revision
-- ids for backlog/rollback (gcloud pubsub schemas commit is separate).
--
--   bq query --use_legacy_sql=false --location=EU < deploy/migrations/20260918_event_campaign_zone.sql

ALTER TABLE `YOUR_GCP_PROJECT.events_raw.events`
  ADD COLUMN IF NOT EXISTS campaign_id STRING,
  ADD COLUMN IF NOT EXISTS zone_id STRING;
