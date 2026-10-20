-- BigQuery bootstrap для ta-events-ingest (раздел 11 спеки).
-- Запуск разово, локально под owner:
--   bq query --use_legacy_sql=false --location=EU < deploy/bigquery.sql
-- Replace YOUR_GCP_PROJECT before applying:
--   sed "s/YOUR_GCP_PROJECT/${PROJECT_ID}/g" deploy/bigquery.sql | bq query --use_legacy_sql=false --location=EU

CREATE SCHEMA IF NOT EXISTS `YOUR_GCP_PROJECT.events_raw`     OPTIONS (location = 'EU');
CREATE SCHEMA IF NOT EXISTS `YOUR_GCP_PROJECT.events_staging` OPTIONS (location = 'EU');
CREATE SCHEMA IF NOT EXISTS `YOUR_GCP_PROJECT.events_marts`   OPTIONS (location = 'EU');

-- Сырьё из Pub/Sub (BigQuery subscription, опции Use topic schema + Write metadata).
-- Колонки event_id..raw повторяют поля topic-схемы (event.avsc) по именам и типам;
-- subscription_name/message_id/publish_time/attributes — метаданные Pub/Sub.
CREATE TABLE IF NOT EXISTS `YOUR_GCP_PROJECT.events_raw.events` (
  event_id   STRING,
  event_name STRING,
  event_time TIMESTAMP,
  received_at TIMESTAMP,
  click_id   STRING,
  country    STRING,
  path       STRING,
  search     STRING,
  ta_source  STRING,
  ta_sub     STRING,
  user_agent STRING,
  ip_hash    STRING,
  url        STRING,
  method     STRING,
  scenario_id          STRING,
  smartlink_variant_id STRING,
  redirect_to          STRING,
  seed       STRING,
  headers JSON,
  cf      JSON,
  params  JSON,
  raw     JSON,
  campaign_id STRING,
  zone_id     STRING,
  cost        STRING,
  subscription_name STRING,
  message_id        STRING,
  publish_time      TIMESTAMP,
  attributes        JSON
)
PARTITION BY DATE(event_time)
CLUSTER BY event_name, country, ta_source
OPTIONS (
  require_partition_filter = true,
  partition_expiration_days = 90
);
