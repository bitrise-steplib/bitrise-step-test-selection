-- Test selection shadow release: queries for the dashboard.
-- `events` stands for the table the step analytics channel (step-analytics.bitrise.io/track)
-- lands in; each row has event_name, a timestamp and the event's properties as JSON.
-- Replace the table and the timestamp conversion with the warehouse's real ones.

-- 1. Saving per workspace and week. Only step runs with readable test results count.
SELECT
  JSON_VALUE(properties, '$.workspace_id')                               AS workspace_id,
  DATE_TRUNC(DATE(event_time), WEEK)                                     AS week,
  COUNT(*)                                                               AS step_runs,
  COUNTIF(JSON_VALUE(properties, '$.decision') = 'selected')             AS runs_with_a_selection,
  ROUND(SUM(CAST(JSON_VALUE(properties, '$.test_run_ms') AS INT64)) / 3.6e6, 1)         AS test_hours,
  -- The real saving lies between these two: test case time only, and the wall time split per unit.
  ROUND(SUM(CAST(JSON_VALUE(properties, '$.estimated_saving_ms') AS INT64)) / 3.6e6, 1)          AS saving_hours_low,
  ROUND(SUM(CAST(JSON_VALUE(properties, '$.estimated_saving_by_units_ms') AS INT64)) / 3.6e6, 1) AS saving_hours_by_units,
  ROUND(SUM(CAST(JSON_VALUE(properties, '$.duration_ms') AS INT64)) / 3.6e6, 2)         AS analysis_hours,
  SAFE_DIVIDE(SUM(CAST(JSON_VALUE(properties, '$.estimated_saving_by_units_ms') AS INT64)),
              SUM(CAST(JSON_VALUE(properties, '$.test_run_ms') AS INT64)))              AS saving_share
FROM events
WHERE event_name = 'test_selection_shadow'
  AND JSON_VALUE(properties, '$.outcomes_known') = 'true'
GROUP BY workspace_id, week
ORDER BY week DESC, saving_hours_by_units DESC;

-- 2. The same overall, per step, to compare Xcode, Gradle and Flutter.
SELECT
  JSON_VALUE(properties, '$.step_id') AS step,
  COUNT(*) AS step_runs,
  SAFE_DIVIDE(SUM(CAST(JSON_VALUE(properties, '$.estimated_saving_by_units_ms') AS INT64)),
              SUM(CAST(JSON_VALUE(properties, '$.test_run_ms') AS INT64))) AS saving_share,
  APPROX_QUANTILES(CAST(JSON_VALUE(properties, '$.duration_ms') AS INT64), 100)[OFFSET(95)] AS analysis_p95_ms
FROM events
WHERE event_name = 'test_selection_shadow' AND JSON_VALUE(properties, '$.outcomes_known') = 'true'
GROUP BY step;

-- 3. Missed failures: every unit a selection would have skipped although a test in it failed.
--    base_build_slug is set for push builds: the unit's tests passed there, so the failure
--    came with the change. Check flaky > 0 and the unit's history before calling it a miss.
SELECT
  event_time,
  JSON_VALUE(properties, '$.workspace_id')    AS workspace_id,
  JSON_VALUE(properties, '$.app_slug')        AS app_slug,
  JSON_VALUE(properties, '$.build_slug')      AS build_slug,
  JSON_VALUE(properties, '$.step_id')         AS step,
  JSON_VALUE(properties, '$.unit')            AS unit,
  JSON_VALUE(properties, '$.mapped')          AS known_to_the_graph,
  JSON_VALUE(properties, '$.failed')          AS failed_tests,
  JSON_VALUE(properties, '$.flaky')           AS flaky_tests,
  JSON_VALUE(properties, '$.base_build_slug') AS base_build_slug
FROM events
WHERE event_name = 'test_selection_missed_failure'
ORDER BY event_time DESC;

-- 4. Miss rate: step runs with a missed failure among the runs that would have skipped something.
SELECT
  DATE_TRUNC(DATE(event_time), WEEK) AS week,
  COUNTIF(JSON_VALUE(properties, '$.missed_failure') = 'true') AS runs_with_a_miss,
  COUNTIF(CAST(JSON_VALUE(properties, '$.skipped_count') AS INT64) > 0) AS runs_that_skip,
  SAFE_DIVIDE(COUNTIF(JSON_VALUE(properties, '$.missed_failure') = 'true'),
              COUNTIF(CAST(JSON_VALUE(properties, '$.skipped_count') AS INT64) > 0)) AS miss_rate
FROM events
WHERE event_name = 'test_selection_shadow' AND JSON_VALUE(properties, '$.decision') = 'selected'
GROUP BY week
ORDER BY week DESC;
