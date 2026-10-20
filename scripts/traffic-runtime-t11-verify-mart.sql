-- One mart row per requested click_id. Join on r.click_id = d.click_id so a
-- missing right row does not reuse the requested key as if it were found.
-- Evaluator requires mart_found and d.click_id plus exact ta_sub.

WITH requested AS (
  SELECT click_id
  FROM UNNEST([{{CLICK_IDS}}]) AS click_id
)
SELECT
  r.click_id AS requested_click_id,
  d.click_id AS click_id,
  d.ta_source,
  d.ta_sub,
  d.campaign_id,
  d.zone_id,
  d.country,
  (d.click_id IS NOT NULL) AS mart_found
FROM requested r
LEFT JOIN `{{PROJECT_ID}}.{{MART_DATASET}}.{{MART_TABLE}}` d
  ON r.click_id = d.click_id
ORDER BY r.click_id;
