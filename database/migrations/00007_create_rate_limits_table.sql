-- +goose Up
-- +goose StatementBegin

-- --------------------------------------------------------
-- Table: public.rate_limits (fixed-window counter; the check runs
-- under advisory locks to synchronize access)
-- --------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.rate_limits (
    key TEXT NOT NULL PRIMARY KEY,
    count INTEGER NOT NULL DEFAULT 0,
    max_requests INTEGER NOT NULL,
    window_seconds INTEGER NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    last_reset_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    -- Only allows lowercase alphanumeric, underscores, and colons
    CONSTRAINT chk_key_format CHECK (key ~ '^[a-z0-9_:]+$')
) USING heap;

CREATE OR REPLACE FUNCTION fn_update_rate_limits_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = clock_timestamp(); RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_rate_limits_updated_at BEFORE UPDATE ON public.rate_limits FOR EACH ROW EXECUTE FUNCTION fn_update_rate_limits_updated_at();

-- Indexes for `public.rate_limits`
-- (the key needs no index: the primary key carries every lookup)

-- --------------------------------------------------------
-- Rate limit check function, returns JSON with rate limit information
-- --------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_check_rate_limit(
  rate_key TEXT,
  max_requests INTEGER,
  window_seconds INTEGER
)
RETURNS JSON AS $$
DECLARE
  now TIMESTAMPTZ := clock_timestamp();
  window_length INTERVAL := make_interval(secs => window_seconds);
  current_count INTEGER;
  reset_at TIMESTAMPTZ;
  remaining INTEGER;
  result JSON;
BEGIN
  -- Get advisory lock to prevent race conditions
  PERFORM pg_advisory_xact_lock(hashtext(rate_key));

  -- Insert or update rate limit record
  INSERT INTO rate_limits (key, count, window_start, max_requests, window_seconds, last_reset_at)
  VALUES (rate_key, 1, now, max_requests, window_seconds, now)
  ON CONFLICT (key) DO UPDATE
  SET
    count = CASE
      WHEN rate_limits.window_start + window_length <= now
        THEN 1
        ELSE rate_limits.count + 1
    END,
    window_start = CASE
      WHEN rate_limits.window_start + window_length <= now
        THEN now
        ELSE rate_limits.window_start
    END,
    max_requests = EXCLUDED.max_requests,
    window_seconds = EXCLUDED.window_seconds,
    last_reset_at = CASE
      WHEN rate_limits.window_start + window_length <= now
        THEN now
        ELSE rate_limits.last_reset_at
    END,
    updated_at = clock_timestamp();

  -- Get current count and calculate reset time
  SELECT count, window_start INTO current_count, reset_at
  FROM rate_limits
  WHERE key = rate_key;

  -- Calculate reset time
  reset_at := reset_at + window_length;

  -- Calculate remaining requests
  remaining := GREATEST(0, max_requests - current_count);

  -- Check if rate limit exceeded
  IF current_count > max_requests THEN
    result := json_build_object(
      'limited', true,
      'limit', max_requests,
      'remaining', 0,
      'reset', EXTRACT(EPOCH FROM reset_at),
      'retry_after', EXTRACT(EPOCH FROM (reset_at - now)),
      'count', current_count
    );

    -- Raise exception with SQL state for rate limit
    RAISE EXCEPTION SQLSTATE '42901' USING
      MESSAGE = 'Rate limit exceeded',
      DETAIL = format('Key: %s, Count: %s, Limit: %s, Retry after: %s seconds',
        rate_key, current_count, max_requests,
        EXTRACT(EPOCH FROM (reset_at - now))::INTEGER),
      HINT = 'Please slow down your requests',
      TABLE = 'public.rate_limits',
      COLUMN = 'count',
      CONSTRAINT = 'chk_rate_limit';
  END IF;

  -- Return success result
  result := json_build_object(
    'limited', false,
    'limit', max_requests,
    'remaining', remaining,
    'reset', EXTRACT(EPOCH FROM reset_at),
    'count', current_count,
    'key', rate_key
  );

  RETURN result;
END;
$$ LANGUAGE plpgsql;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_rate_limits_updated_at ON public.rate_limits;


DROP TABLE IF EXISTS public.rate_limits;

DROP FUNCTION IF EXISTS fn_check_rate_limit(TEXT, INTEGER, INTEGER);
DROP FUNCTION IF EXISTS fn_update_rate_limits_updated_at();

-- +goose StatementEnd
