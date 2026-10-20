-- ELT marts для ta-events-ingest (раздел 7 спеки): витрина-воронка по click_id.
-- Склейка цепочки одного захода (ingress -> scenario_assigned -> redirect|pageview)
-- по click_id из staging (уже дедуп + флаг бота). MVP — VIEW; в проде поверх
-- инкрементального staging и с окном опоздавших событий.
-- Запуск: bq query --use_legacy_sql=false --location=EU < deploy/elt_marts.sql

CREATE OR REPLACE VIEW `YOUR_GCP_PROJECT.events_marts.click_funnel` AS
SELECT
  click_id,
  ANY_VALUE(country)   AS country,
  ANY_VALUE(ta_source)    AS ta_source,
  ANY_VALUE(ta_sub)       AS ta_sub,
  ANY_VALUE(campaign_id)  AS campaign_id,
  ANY_VALUE(zone_id)      AS zone_id,
  MIN(event_time)      AS first_event_time,
  MAX(IF(event_name = 'ingress', 1, 0))                            AS got_ingress,
  MAX(IF(event_name = 'scenario_assigned', scenario_id, NULL))     AS scenario_id,
  MAX(IF(event_name = 'redirect', smartlink_variant_id, NULL))     AS smartlink_variant_id,
  MAX(IF(event_name = 'redirect', redirect_to, NULL))              AS redirect_to,
  MAX(IF(event_name = 'redirect', 1, 0))                           AS redirected,
  MAX(IF(event_name = 'pageview', 1, 0))                           AS pageview
FROM `YOUR_GCP_PROJECT.events_staging.events`
WHERE NOT is_bot
GROUP BY click_id;
