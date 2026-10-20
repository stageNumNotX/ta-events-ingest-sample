-- Isolated T11 raw table for owner-run Pub/Sub → BigQuery export.
-- Do not apply this file to the production ingest raw dataset/table.
-- Substituted by scripts/traffic-runtime-t11-setup.sh:
--   {{PROJECT_ID}} {{DATASET}} {{TABLE}} {{LOCATION}} {{TTL_DAYS}}
--
-- Ordering: create this table BEFORE the BigQuery subscription.
-- Partition filter is required; verification queries must bound bytes billed.

CREATE SCHEMA IF NOT EXISTS `{{PROJECT_ID}}.{{DATASET}}`
OPTIONS (location = '{{LOCATION}}');

CREATE TABLE IF NOT EXISTS `{{PROJECT_ID}}.{{DATASET}}.{{TABLE}}` (
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
CLUSTER BY event_name, ta_source
OPTIONS (
  require_partition_filter = true,
  partition_expiration_days = {{TTL_DAYS}}
);
