-- ELT staging для ta-events-ingest (раздел 7 спеки).
-- Дедуп по event_id (Pub/Sub at-least-once) + заготовка флага бота + типизация.
-- Для MVP реализовано как VIEW поверх raw (без планировщика); в проде — переключить
-- на инкрементальную таблицу с MERGE по свежим партициям event_time (Scheduled Query / dbt).
-- Запуск: bq query --use_legacy_sql=false --location=EU < deploy/elt_staging.sql

CREATE OR REPLACE VIEW `YOUR_GCP_PROJECT.events_staging.events` AS
SELECT * EXCEPT (rn)
FROM (
  SELECT
    event_id,
    event_name,
    event_time,
    received_at,
    click_id,
    country,
    path,
    search,
    ta_source,
    ta_sub,
    user_agent,
    ip_hash,
    url,
    method,
    scenario_id,
    smartlink_variant_id,
    redirect_to,
    seed,
    headers,
    cf,
    params,
    raw,
    campaign_id,
    zone_id,
    publish_time,
    -- Заготовка эвристики бота; настоящая логика/семплирование — в ELT, не в сервисе.
    REGEXP_CONTAINS(LOWER(user_agent), r'(bot|crawler|spider|crawl|slurp|headless)') AS is_bot,
    ROW_NUMBER() OVER (PARTITION BY event_id ORDER BY event_time) AS rn
  FROM `YOUR_GCP_PROJECT.events_raw.events`
  -- require_partition_filter на raw: ограничиваем окно (допускаем опоздавшие события).
  WHERE event_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 90 DAY)
)
WHERE rn = 1;
