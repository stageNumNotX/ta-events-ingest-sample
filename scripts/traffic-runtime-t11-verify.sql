-- Bounded T11 check: one row per event for each requested click_id (LEFT JOIN).
-- Incomplete clicks stay visible — no HAVING filter that would hide them.
-- Deduplicate at-least-once Pub/Sub copies by event_id.
-- Partition filter is mandatory (require_partition_filter=true).
-- Values are returned as stored (not COUNTIF-present). The evaluator asserts
-- exact ta_source / ta_sub / campaign_id / zone_id / country /
-- contract_release_sha256 / k_revision on every event, and exactly the
-- ingress + scenario_assigned + redirect triple.

WITH requested AS (
  SELECT click_id
  FROM UNNEST([{{CLICK_IDS}}]) AS click_id
),
deduped AS (
  SELECT
    click_id,
    event_id,
    event_name,
    ta_source,
    ta_sub,
    campaign_id,
    zone_id,
    country,
    JSON_VALUE(params, '$.contract_release_sha256') AS contract_release_sha256,
    JSON_VALUE(params, '$.k_revision') AS k_revision
  FROM `{{PROJECT_ID}}.{{DATASET}}.{{TABLE}}`
  WHERE DATE(event_time) >= DATE('{{WINDOW_START}}')
    AND click_id IN UNNEST([{{CLICK_IDS}}])
  QUALIFY ROW_NUMBER() OVER (PARTITION BY event_id ORDER BY publish_time) = 1
)
SELECT
  r.click_id,
  d.event_id,
  d.event_name,
  d.ta_source,
  d.ta_sub,
  d.campaign_id,
  d.zone_id,
  d.country,
  d.contract_release_sha256,
  d.k_revision
FROM requested r
LEFT JOIN deduped d ON r.click_id = d.click_id
ORDER BY r.click_id, d.event_name;
