-- 0002_report_counts.sql: a report's spend also states how many runs actually ran and
-- how many were answered by the baseline cache (spec FR-016).
ALTER TABLE reports ADD COLUMN spent_runs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE reports ADD COLUMN cache_hits INTEGER NOT NULL DEFAULT 0;
