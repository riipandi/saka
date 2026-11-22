--
-- Pocket ID database schema reference (schema-only dump)
--
-- Source        : live instance "pocketid" (see container/compose-dev.yaml service `pocketid`)
-- Image         : ghcr.io/pocket-id/pocket-id:v2 (docker sha256:83cfbfafd21a)
-- Upstream tag  : v2.14.0 (.version = 2.14.0)
-- Cut-off       : migration 20260814120000_api_client_access (schema_migrations holds
--                 exactly this one version; squashed baseline, dirty=false)
-- Dumped        : 2026-09-14 with pg_dump 18.6 (schema-only, --no-owner --no-privileges)
-- Extensions    : citext, pgcrypto
-- Engine        : PostgreSQL 18.6 (postgres:18-alpine container)
--
-- Notes:
--   - `francis_*` tables are the riverqueue/river job-queue schema, not Pocket ID domain.
--   - `kv` holds the encrypted JWT private key and instance id — no data is included here.
--   - All Pocket ID domain tables are created by the single squashed migration above;
--     the full history lives in backend/resources/migrations/postgres at the tag.
--
-- PostgreSQL database dump
--

\restrict bbssT8JLdvbGp4WtA6DUmc8Nwbx0LFSNRchjlVwuNSZiCOj6DuccPYaa1lwAlwp

-- Dumped from database version 18.6
-- Dumped by pg_dump version 18.6

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: citext; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;


--
-- Name: EXTENSION citext; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION citext IS 'data type for case-insensitive character strings';


--
-- Name: pgcrypto; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;


--
-- Name: EXTENSION pgcrypto; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pgcrypto IS 'cryptographic functions';


--
-- Name: francis_active_actors_delete_update_alarms_fn(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_active_actors_delete_update_alarms_fn() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- Batch update: join alarms with the transition table of deleted rows
    UPDATE francis_alarms
    SET
        alarm_lease_id = NULL,
        alarm_lease_expiration_time = NULL
    FROM old_rows r
    WHERE
        francis_alarms.actor_type = r.actor_type
        AND francis_alarms.actor_id = r.actor_id
        AND francis_alarms.alarm_lease_id IS NOT NULL;

    -- Statement-level trigger return value is ignored
    RETURN NULL;
END;
$$;


--
-- Name: francis_actor_active_v1(text, text, timestamp without time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_actor_active_v1(p_actor_type text, p_actor_id text, p_health_cutoff timestamp without time zone) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1
        FROM francis_active_actors AS aa
        INNER JOIN francis_hosts AS h ON
            aa.host_id = h.host_id
        WHERE
            aa.actor_type = p_actor_type 
            AND aa.actor_id = p_actor_id
            AND h.host_last_health_check >= p_health_cutoff
    );
END;
$$;


--
-- Name: francis_fetch_and_lease_upcoming_alarms_v1(uuid[], interval, interval, interval, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_fetch_and_lease_upcoming_alarms_v1(p_host_ids uuid[], p_host_health_check_deadline interval, p_alarms_fetch_ahead_interval interval, p_alarms_lease_duration interval, p_batch_size integer) RETURNS TABLE(r_alarm_id uuid, r_actor_type text, r_actor_id text, r_alarm_name text, r_alarm_due_time timestamp without time zone, r_lease_id uuid)
    LANGUAGE plpgsql
    AS $$
DECLARE
    -- All time variables are UTC
    v_now timestamp;
    v_horizon timestamp;
    v_health_cutoff timestamp;
    v_lease_expiration timestamp;
    v_allocated_host_id uuid;
    v_actor_lock_key bigint;
    rec RECORD;
BEGIN
    -- Initialize time variables
    -- now() is converted to UTC so all time values in this function are UTC timestamps
    v_now := now() AT TIME ZONE 'utc';
    v_horizon := v_now + p_alarms_fetch_ahead_interval;
    v_health_cutoff := v_now - p_host_health_check_deadline;
    v_lease_expiration := v_now + p_alarms_lease_duration;

    -- Create temporary table for active hosts with their capacities
    CREATE TEMPORARY TABLE temp_active_hosts (
        host_id uuid NOT NULL,
        actor_type text NOT NULL,
        actor_idle_timeout interval NOT NULL,
        concurrency_limit integer NOT NULL,
        available_capacity integer NOT NULL,
        PRIMARY KEY (host_id, actor_type)
    ) ON COMMIT DROP;

    -- Populate the temporary table with active hosts and their available capacities
    WITH
        current_count AS (
            SELECT aa.host_id, aa.actor_type, COUNT(*) AS active_count
            FROM francis_active_actors AS aa
            GROUP BY aa.host_id, aa.actor_type
        )
    INSERT INTO temp_active_hosts
        (host_id, actor_type, actor_idle_timeout, concurrency_limit, available_capacity)
    SELECT 
        hat.host_id,
        hat.actor_type,
        hat.actor_idle_timeout,
        COALESCE(hat.actor_concurrency_limit, 0) AS concurrency_limit,
        CASE 
            -- If there's no limit, assume it's MaxInt32
            WHEN hat.actor_concurrency_limit = 0 THEN 2147483647 - COALESCE(current_count.active_count, 0)
            ELSE GREATEST(0, hat.actor_concurrency_limit - COALESCE(current_count.active_count, 0))
        END AS available_capacity
    FROM francis_host_actor_types AS hat
    INNER JOIN francis_hosts h ON
        hat.host_id = h.host_id
    LEFT JOIN current_count ON
        hat.host_id = current_count.host_id
        AND hat.actor_type = current_count.actor_type
    WHERE
        h.host_last_health_check >= v_health_cutoff
        AND h.host_id = ANY(p_host_ids);

    -- Early return if no active hosts found
    IF NOT EXISTS (SELECT 1 FROM temp_active_hosts) THEN
        RETURN;
    END IF;

    -- Create temporary table for upcoming alarms
    CREATE TEMPORARY TABLE temp_upcoming_alarms (
        alarm_id uuid NOT NULL,
        actor_type text NOT NULL,
        actor_id text NOT NULL,
        alarm_name text NOT NULL,
        alarm_due_time timestamp NOT NULL,
        existing_host_id uuid,
        allocated_host_id uuid
    ) ON COMMIT DROP;

    -- Fetch upcoming alarms based on whether we have capacity constraints
    IF EXISTS (
        SELECT 1 FROM temp_active_hosts
        WHERE concurrency_limit > 0
    ) THEN
        -- How the query works:
        --
        -- 1. allowed_actor_hosts:
        --    This CTE is necessary to look up what hosts are ok when we see that the actor mapped to an alarm is active.
        --    Some alarms are for actors that are not active, but some may be mapped to actors that are already active.
        --    We accept alarms mapping to an active actor if they either:
        --      - Map to an actor that's active on a host in the allowlist
        --      - Map to an actor that's active on an unhealthy host
        --    The CTE loads host IDs both from the pre-filtered allowlist (the temp_active_hosts table), and from the
        --    active_actors table, looking at all the actors of the types we care about (those that can be executed on
        --    the hosts in the allowlist).
        -- 2. actor_type_capacity:
        --    This CTE computes the sum of the available capacity for each actor type, across all hosts in the allowlist.
        -- 3. ranked_alarms:
        --    This CTE looks up the list of alarms and assigns a "rank".
        --    Alarms are filtered by:
        --      - Limiting to the actor types that can be executed on the hosts in the request and for which we have any
        --        capacity (this is done with the INNER JOIN on actor_type_capacity)
        --      - Ensuring their due time is within the horizon we are considering
        --      - Selecting alarms that aren't leased, or whose lease has expired
        --      - Selecting alarms that are not tied to an active actor, or whose actor is in the allowed_actor_hosts list
        --    Among all the filtered alarms, it assigns a "rank" which is the ROW_NUMBER(). This is sorted by the due time
        --    and partitioned by actor type. It looks at all filtered alarms (more than the batch size, but still within
        --    the time horizon and with the other filters listed above), and assigns a rank for each actor type: for example,
        --    if we have capacity for alarms of types A and B, the earliest alarm of type A will have rank 1, and so will
        --    the earliest of type B.
        --    There's one exception, which is that when the alarm is for an actor that's already active on a host in the
        --    allowlist, we assign it a rank of 0, as it doesn't use more capacity.
        --    This "ranking" selects a lot or rows, and it's the reason why this is the "slow" path.
        -- 4. Finally, select from the ranked list, returning alarms for which there's sufficient capacity, in order of
        --    of execution time. We do this by excluding the rows from ranked_alarms in which the row number is greater than the
        --    capacity left (e.g. if we have capacity for only 4 actors of type A, ranked rows for type A with row number
        --    greater than 4 are excluded).
        --
        -- Alarms that have an active actor will have host_id non-null. However, that will be non-null also for actors that
        -- are on un-healthy hosts; we will need to filter them out in the Go code later.
        INSERT INTO temp_upcoming_alarms
            (alarm_id, actor_type, actor_id, alarm_name, alarm_due_time, existing_host_id)
        WITH
            allowed_actor_hosts AS (
                SELECT tah.host_id FROM temp_active_hosts AS tah

                UNION

                SELECT aa.host_id
                FROM francis_active_actors AS aa
                INNER JOIN temp_active_hosts AS tah ON
                    aa.actor_type = tah.actor_type
                INNER JOIN francis_hosts AS h ON
                    aa.host_id = h.host_id
                WHERE
                    h.host_last_health_check < v_health_cutoff
            ),
            actor_type_capacity AS (
                SELECT tah.actor_type, LEAST(SUM(tah.available_capacity), p_batch_size) AS total_capacity
                FROM temp_active_hosts AS tah
                GROUP BY tah.actor_type
            ),
            ranked_alarms AS (
                SELECT 
                    a.alarm_id, a.actor_type, a.actor_id,
                    a.alarm_name, a.alarm_due_time,
                    aa.host_id AS existing_host_id,
                    atc.total_capacity,
                    CASE
                        WHEN
                            aa.host_id IS NULL
                            OR NOT EXISTS (
                                SELECT 1 FROM temp_active_hosts WHERE temp_active_hosts.host_id = aa.host_id
                            )
                        THEN 0
                        ELSE 1
                        END AS active_actor,
                    SUM(1)
                        FILTER (
                            WHERE aa.host_id IS NULL
                            OR NOT EXISTS (SELECT 1 FROM temp_active_hosts WHERE temp_active_hosts.host_id = aa.host_id)
                        )
                        OVER (
                            PARTITION BY a.actor_type
                            ORDER BY a.alarm_due_time, a.alarm_id
                            ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
                        ) AS rownum
                FROM francis_alarms AS a
                INNER JOIN actor_type_capacity AS atc ON
                    a.actor_type = atc.actor_type
                LEFT JOIN francis_active_actors AS aa ON
                    a.actor_type = aa.actor_type
                    AND a.actor_id = aa.actor_id
                WHERE
                    a.alarm_due_time <= v_horizon
                    AND (
                        a.alarm_lease_id IS NULL
                        OR a.alarm_lease_expiration_time IS NULL
                        OR a.alarm_lease_expiration_time < v_now
                    )
                    AND (
                        aa.host_id IS NULL
                        OR EXISTS (
                            SELECT 1
                            FROM allowed_actor_hosts AS ah
                            WHERE ah.host_id = aa.host_id
                        )
                    )
            )
        SELECT 
            alarm_id,
            actor_type,
            actor_id,
            alarm_name,
            alarm_due_time,
            existing_host_id
        FROM ranked_alarms
        WHERE
            active_actor = 1
            OR rownum <= total_capacity
        ORDER BY alarm_due_time, alarm_id
        LIMIT p_batch_size;
    ELSE
        -- How the query works:
        --
        -- 1. allowed_actor_hosts:
        --    This CTE is necessary to look up what hosts are ok when we see that the actor mapped to an alarm is active.
        --    Some alarms are for actors that are not active, but some may be mapped to actors that are already active.
        --    We accept alarms mapping to an active actor if they either:
        --      - Map to an actor that's active on a host in the allowlist
        --      - Map to an actor that's active on an unhealthy host
        --    The CTE loads host IDs both from the pre-filtered allowlist (the temp_active_hosts table), and from the
        --    active_actors table, looking at all the actors of the types we care about (those that can be executed on
        --    the hosts in the allowlist).
        -- 2. Look up alarms:
        --    Next, we can look up the list of alarms, looking at the N-most alarms that are coming up the soonest.
        --    We filter the alarms by:
        --      - Limiting to the actor types that can be executed on the hosts in the request
        --        (this is done with the INNER JOIN on temp_active_hosts)
        --      - Ensuring their due time is within the horizon we are considering
        --      - Selecting alarms that aren't leased, or whose lease has expired
        --      - Selecting alarms that are not tied to an active actor, or whose actor is in the allowed_actor_hosts list
        --
        -- Alarms that have an active actor will have host_id non-null. However, that will be non-null also for actors that
        -- are on un-healthy hosts; we will need to filter them out later.
        INSERT INTO temp_upcoming_alarms
            (alarm_id, actor_type, actor_id, alarm_name, alarm_due_time, existing_host_id)
        WITH
            allowed_actor_hosts AS (
                SELECT tah.host_id FROM temp_active_hosts AS tah
                UNION
                SELECT aa.host_id
                FROM francis_active_actors AS aa
                INNER JOIN temp_active_hosts AS tah ON
                    aa.actor_type = tah.actor_type
                INNER JOIN francis_hosts AS h ON
                    aa.host_id = h.host_id
                WHERE
                    h.host_last_health_check < v_health_cutoff
        )
        SELECT 
            a.alarm_id, a.actor_type, a.actor_id,
            a.alarm_name, a.alarm_due_time,
            aa.host_id AS existing_host_id
        FROM francis_alarms AS a
        INNER JOIN temp_active_hosts AS tah ON
            a.actor_type = tah.actor_type
        LEFT JOIN francis_active_actors AS aa ON
            a.actor_type = aa.actor_type
            AND a.actor_id = aa.actor_id
        WHERE
            a.alarm_due_time <= v_horizon
            AND (
                a.alarm_lease_id IS NULL
                OR a.alarm_lease_expiration_time IS NULL
                OR a.alarm_lease_expiration_time < v_now
            )
            AND (
                aa.host_id IS NULL
                OR EXISTS (
                    SELECT 1
                    FROM allowed_actor_hosts AS ah
                    WHERE ah.host_id = aa.host_id
                )
            )
        ORDER BY a.alarm_due_time, a.alarm_id
        LIMIT p_batch_size;
    END IF;

    -- Filter out alarms with actors on unhealthy hosts
    UPDATE temp_upcoming_alarms 
    SET existing_host_id = NULL 
    WHERE
        existing_host_id IS NOT NULL 
        AND NOT EXISTS (
            SELECT 1 FROM temp_active_hosts AS tah
            WHERE
                tah.host_id = temp_upcoming_alarms.existing_host_id
                AND tah.actor_type = temp_upcoming_alarms.actor_type
        );

    -- Early return if no alarms found
    IF NOT EXISTS(SELECT 1 FROM temp_upcoming_alarms) THEN
        RETURN;
    END IF;

    -- Allocate actors for alarms that don't have existing actors
    FOR rec IN 
        SELECT tua.alarm_id, tua.actor_type, tua.actor_id, tua.alarm_due_time
        FROM temp_upcoming_alarms AS tua 
        WHERE tua.existing_host_id IS NULL
    LOOP
        -- Create deterministic lock key from actor type and ID to prevent double activation
        v_actor_lock_key := abs(francis_h_bigint(rec.actor_type || '::' || rec.actor_id));

        -- Try to acquire a transaction-level advisory lock for this specific actor.
        -- This may fail if someone else is already holding a lock for the actor: it means the actor is being activated somewhere else, likely because it's going to be invoked (which will keep it busy for a bit).
        -- To keep things simple, we just skip this alarm and we will re-fetch it on the next iteration.
        -- The alternative would be to block while waiting for the lock, but that would slow everything down.
        -- We use the transaction-level variant (pg_try_advisory_xact_lock) so the lock is released automatically when the function's transaction ends, including on error or query cancellation.
        -- This avoids leaking session-level advisory locks onto pooled connections (which could otherwise make an actor permanently un-allocatable on that connection).
        IF pg_try_advisory_xact_lock(v_actor_lock_key) THEN
            -- Check if actor already exists (another process might have created it)
            IF NOT francis_actor_active_v1(rec.actor_type, rec.actor_id, v_health_cutoff) THEN
                    -- Find a random host with capacity for this actor type
                    -- Note: There's a chance that multiple queries may allocate actors on the same hosts and we may go over capacity
                    -- We consider this an acceptable risk, as the complexity of handling that case is too significant otherwise (it would require locking the row with a FOR UPDATE in active_actor_hosts, which can lead to deadlocks and other issues)
                    SELECT host_id INTO v_allocated_host_id
                    FROM temp_active_hosts 
                    WHERE
                        actor_type = rec.actor_type 
                        AND available_capacity > 0
                    ORDER BY 
                        -- Prefer hosts with lower current load for better distribution
                        available_capacity DESC,
                        -- Then randomize among hosts with same load
                        random()
                    LIMIT 1;

                    IF v_allocated_host_id IS NOT NULL THEN
                        -- Insert/update the actor
                        -- Note that we perform an upsert query here. This is because the actor (with same type and ID) may already be present in the table, where it's active on a host that has failed (but hasn't been garbage-collected yet)
                        INSERT INTO francis_active_actors
                            (actor_type, actor_id, host_id, actor_idle_timeout, actor_activation)
                        SELECT
                            rec.actor_type, 
                            rec.actor_id, 
                            v_allocated_host_id, 
                            tah.actor_idle_timeout,
                            -- We set the alarm's due time as actor activation time, or the current time if that's later
                            GREATEST(rec.alarm_due_time, v_now)
                        FROM temp_active_hosts AS tah 
                        WHERE
                            tah.host_id = v_allocated_host_id
                            AND tah.actor_type = rec.actor_type
                        ON CONFLICT (actor_type, actor_id) DO UPDATE SET
                            host_id = EXCLUDED.host_id,
                            actor_idle_timeout = EXCLUDED.actor_idle_timeout,
                            actor_activation = EXCLUDED.actor_activation;

                        -- Update the allocated host in our temp table
                        UPDATE temp_upcoming_alarms 
                        SET allocated_host_id = v_allocated_host_id 
                        WHERE alarm_id = rec.alarm_id;

                        -- Decrease available capacity
                        UPDATE temp_active_hosts 
                        SET
                            available_capacity = available_capacity - 1 
                        WHERE 
                            host_id = v_allocated_host_id
                            AND actor_type = rec.actor_type;

                        -- Clear the variable for next iteration
                        v_allocated_host_id := NULL;
                    END IF; -- End of v_allocated_host_id not null
            END IF;  -- End of actor existence check
        END IF;  -- End of advisory lock acquisition
    END LOOP;

    -- Finally, acquire leases on all alarms that have actors (existing or allocated)
    RETURN QUERY
    UPDATE francis_alarms
    SET
        alarm_lease_id = gen_random_uuid(),
        alarm_lease_expiration_time = v_lease_expiration
    FROM temp_upcoming_alarms AS tua
    WHERE
        francis_alarms.alarm_id = tua.alarm_id
        AND (
            tua.existing_host_id IS NOT NULL
            OR tua.allocated_host_id IS NOT NULL
        )
        AND (
            francis_alarms.alarm_lease_id IS NULL
            OR francis_alarms.alarm_lease_expiration_time IS NULL
            OR francis_alarms.alarm_lease_expiration_time < v_now
        )
    RETURNING
        francis_alarms.alarm_id AS r_alarm_id,
        francis_alarms.actor_type AS r_actor_type,
        francis_alarms.actor_id AS r_actor_id,
        francis_alarms.alarm_name AS r_alarm_name,
        francis_alarms.alarm_due_time AS r_alarm_due_time,
        francis_alarms.alarm_lease_id AS r_lease_id;

END;
$$;


--
-- Name: francis_h_bigint(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_h_bigint(text) RETURNS bigint
    LANGUAGE sql
    AS $_$
    SELECT ('x' || substr(encode(sha256($1::bytea), 'hex'), 1, 16))::bit(64)::bigint;
$_$;


--
-- Name: francis_lookup_active_actor_v1(text, text, interval, uuid[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_lookup_active_actor_v1(p_actor_type text, p_actor_id text, p_host_health_check_deadline interval, p_allowed_hosts uuid[] DEFAULT NULL::uuid[]) RETURNS TABLE(host_id uuid, host_address text, idle_timeout interval)
    LANGUAGE plpgsql
    AS $$
DECLARE
    v_host_id uuid;
    v_host_address text;
    v_idle_timeout interval;
BEGIN
    -- Check if the actor is already active on any healthy host
    SELECT h.host_id, h.host_address, aa.actor_idle_timeout
    INTO v_host_id, v_host_address, v_idle_timeout
    FROM francis_active_actors AS aa
    JOIN francis_hosts AS h ON aa.host_id = h.host_id
    WHERE
        aa.actor_type = p_actor_type
        AND aa.actor_id = p_actor_id
        AND h.host_last_health_check >= ((now() AT TIME ZONE 'utc') - p_host_health_check_deadline);

    -- If we found an active actor
    IF FOUND THEN
        -- Check host restrictions if any are specified
        IF p_allowed_hosts IS NOT NULL AND array_length(p_allowed_hosts, 1) > 0 THEN
            -- Check if the current host is in the allowed list
            IF v_host_id = ANY(p_allowed_hosts) THEN
                -- Host is allowed, return the result
                host_id := v_host_id;
                host_address := v_host_address;
                idle_timeout := v_idle_timeout;
                RETURN NEXT;
                RETURN;
            ELSE
                -- Actor is on a disallowed host, raise an exception
                RAISE EXCEPTION 'NO_HOST_AVAILABLE' USING ERRCODE = 'P0001';
            END IF;
        ELSE
            -- No host restrictions, return the active actor
            host_id := v_host_id;
            host_address := v_host_address;
            idle_timeout := v_idle_timeout;
            RETURN NEXT;
            RETURN;
        END IF;
    END IF;

    -- If we reach here, no active actor was found
    RETURN;
END;
$$;


--
-- Name: francis_lookup_allocate_actor_v1(text, text, interval, uuid[]); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.francis_lookup_allocate_actor_v1(p_actor_type text, p_actor_id text, p_host_health_check_deadline interval, p_allowed_hosts uuid[] DEFAULT NULL::uuid[]) RETURNS TABLE(host_id uuid, host_address text, idle_timeout interval)
    LANGUAGE plpgsql
    AS $$
DECLARE
    v_host_id uuid;
    v_host_address text;
    v_idle_timeout interval;
    v_lock_key bigint;
    v_result RECORD;
BEGIN
    -- First, check if the actor is already active on any healthy host
    SELECT * INTO v_result 
    FROM francis_lookup_active_actor_v1(p_actor_type, p_actor_id, p_host_health_check_deadline, p_allowed_hosts);

    IF FOUND THEN
        host_id := v_result.host_id;
        host_address := v_result.host_address;
        idle_timeout := v_result.idle_timeout;
        RETURN NEXT;
        RETURN;
    END IF;

    -- Create a deterministic lock key based on actor type and ID
    -- This ensures the same actor always gets the same lock
    v_lock_key := abs(francis_h_bigint(p_actor_type || '::' || p_actor_id));

    -- Acquire an advisory lock for this specific actor
    -- This prevents concurrent placement of the same actor
    PERFORM pg_advisory_xact_lock(v_lock_key);

    -- Check again if the actor is already active, as it may have gotten activated since we got the lock
    SELECT * INTO v_result 
    FROM francis_lookup_active_actor_v1(p_actor_type, p_actor_id, p_host_health_check_deadline, p_allowed_hosts);

    IF FOUND THEN
        host_id := v_result.host_id;
        host_address := v_result.host_address;
        idle_timeout := v_result.idle_timeout;
        RETURN NEXT;
        RETURN;
    END IF;

    -- If we reach here, we need to find a suitable host and activate the actor
    -- Note that because we load the current count without locks, there's a chance that with many concurrent requests
    -- for actor activation, we may exceed the capacity on a host.
    -- We consider enforcing capacity constraints as a best-effort thing: adding stricter checks would be possible,
    -- but would add significant complexity and increases the risk of deadlocks.
    WITH
        current_count AS (
            -- Count actual rows instead of using the view to avoid race conditions
            SELECT aa.host_id, COUNT(*) AS active_count
            FROM francis_active_actors AS aa
            WHERE aa.actor_type = p_actor_type
            GROUP BY aa.host_id
        ),
        available_hosts AS (
            SELECT
                h.host_id,
                h.host_address,
                hat.actor_idle_timeout,
                hat.actor_concurrency_limit,
                COALESCE(current_count.active_count, 0) AS current_active_count
            FROM francis_hosts AS h
            INNER JOIN francis_host_actor_types AS hat ON h.host_id = hat.host_id
            LEFT JOIN current_count ON h.host_id = current_count.host_id
            WHERE
                hat.actor_type = p_actor_type
            AND h.host_last_health_check >= ((now() AT TIME ZONE 'utc') - p_host_health_check_deadline)
            AND (
                hat.actor_concurrency_limit = 0 
                OR COALESCE(current_count.active_count, 0) < hat.actor_concurrency_limit
            )
            AND (
                p_allowed_hosts IS NULL 
                OR array_length(p_allowed_hosts, 1) = 0 
                OR h.host_id = ANY(p_allowed_hosts)
            )
            ORDER BY 
                -- Prefer hosts with lower current load for better distribution
                current_active_count ASC,
                -- Then randomize among hosts with same load
                random()
            LIMIT 1
        )
    SELECT ah.host_id, ah.host_address, ah.actor_idle_timeout
    INTO v_host_id, v_host_address, v_idle_timeout
    FROM available_hosts AS ah;

    -- If no suitable host was found
    IF NOT FOUND THEN
        RAISE EXCEPTION 'NO_HOST_AVAILABLE' USING ERRCODE = 'P0001';
    END IF;

    -- Finally, insert the row in the active actors table to "activate" the actor, in the host we selected
	-- Note that we perform an upsert query here. This is because the actor (with same type and ID) may already be present in the table, where it's active on a host that has failed (but hasn't been garbage-collected yet)
    INSERT INTO francis_active_actors (
        actor_type,
        actor_id,
        host_id,
        actor_idle_timeout,
        actor_activation
    ) VALUES (
        p_actor_type,
        p_actor_id,
        v_host_id,
        v_idle_timeout,
        -- Stored as UTC
        now() AT TIME ZONE 'utc'
    )
    ON CONFLICT (actor_type, actor_id) DO UPDATE SET
        host_id = EXCLUDED.host_id,
        actor_idle_timeout = EXCLUDED.actor_idle_timeout,
        actor_activation = EXCLUDED.actor_activation;

    -- Return the selected host information
    host_id := v_host_id;
    host_address := v_host_address;
    idle_timeout := v_idle_timeout;
    RETURN NEXT;

    -- Advisory lock is automatically released at transaction end
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_keys (
    id uuid NOT NULL,
    name character varying(255) NOT NULL,
    key character varying(255) NOT NULL,
    description text,
    expires_at timestamp with time zone NOT NULL,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone,
    user_id uuid,
    expiration_email_sent boolean DEFAULT false NOT NULL
);


--
-- Name: api_permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_permissions (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    api_id uuid NOT NULL,
    key text NOT NULL,
    name text NOT NULL,
    description text,
    allowed_for_cimd_clients boolean DEFAULT false NOT NULL
);


--
-- Name: apis; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apis (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone,
    name text NOT NULL,
    audience text NOT NULL,
    allow_cimd_clients boolean DEFAULT false NOT NULL
);


--
-- Name: audit_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_logs (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    event character varying(100) NOT NULL,
    ip_address inet,
    data jsonb NOT NULL,
    user_id uuid,
    user_agent text,
    country character varying(100),
    city character varying(100)
);


--
-- Name: custom_claims; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.custom_claims (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    key character varying(255) NOT NULL,
    value text NOT NULL,
    user_id uuid,
    user_group_id uuid,
    CONSTRAINT custom_claims_check CHECK (((user_id IS NOT NULL) OR (user_group_id IS NOT NULL)))
);


--
-- Name: francis_active_actors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_active_actors (
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    host_id uuid NOT NULL,
    actor_idle_timeout interval NOT NULL,
    actor_activation timestamp without time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL
);


--
-- Name: francis_actor_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_actor_state (
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    actor_state_data bytea NOT NULL,
    actor_state_expiration_time timestamp without time zone
);


--
-- Name: francis_alarms; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_alarms (
    alarm_id uuid DEFAULT gen_random_uuid() NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    alarm_name text NOT NULL,
    alarm_due_time timestamp without time zone NOT NULL,
    alarm_interval text,
    alarm_ttl_time timestamp without time zone,
    alarm_data bytea,
    alarm_lease_id uuid,
    alarm_lease_expiration_time timestamp without time zone,
    alarm_kind text DEFAULT 'alarm'::text NOT NULL,
    job_method text,
    alarm_cron text
);


--
-- Name: francis_cluster_config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_cluster_config (
    cluster_config_id integer NOT NULL,
    max_hosts integer,
    exclusive_owner text,
    exclusive_expires_at bigint,
    CONSTRAINT francis_cluster_config_cluster_config_id_check CHECK ((cluster_config_id = 1))
);


--
-- Name: francis_consumed_join_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_consumed_join_tokens (
    join_token text NOT NULL,
    host_id uuid NOT NULL,
    expires_at timestamp without time zone NOT NULL
);


--
-- Name: francis_dead_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_dead_jobs (
    job_id uuid NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    job_method text NOT NULL,
    job_data bytea,
    attempts integer NOT NULL,
    last_error text,
    failed_at timestamp without time zone NOT NULL,
    original_due timestamp without time zone NOT NULL,
    job_interval text,
    job_cron text
);


--
-- Name: francis_host_active_actor_count; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.francis_host_active_actor_count AS
 SELECT host_id,
    actor_type,
    count(*) AS active_count
   FROM public.francis_active_actors
  GROUP BY host_id, actor_type;


--
-- Name: francis_host_actor_types; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_host_actor_types (
    host_id uuid NOT NULL,
    actor_type text NOT NULL,
    actor_idle_timeout interval NOT NULL,
    actor_concurrency_limit integer DEFAULT 0 NOT NULL
);


--
-- Name: francis_hosts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_hosts (
    host_id uuid DEFAULT gen_random_uuid() NOT NULL,
    host_address text NOT NULL,
    host_last_health_check timestamp without time zone DEFAULT (now() AT TIME ZONE 'utc'::text) NOT NULL
);


--
-- Name: francis_metadata; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.francis_metadata (
    key text NOT NULL,
    value text NOT NULL
);


--
-- Name: interaction_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.interaction_sessions (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    consent_required boolean DEFAULT false NOT NULL,
    reauthentication_required boolean DEFAULT false NOT NULL,
    authentication_required boolean DEFAULT false NOT NULL,
    account_selection_required boolean DEFAULT false NOT NULL,
    scopes jsonb DEFAULT '[]'::jsonb NOT NULL,
    client_id text NOT NULL,
    user_id uuid,
    requested_at timestamp with time zone NOT NULL,
    reauthenticated_at timestamp with time zone,
    parameters jsonb DEFAULT '{}'::jsonb NOT NULL
);


--
-- Name: kv; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.kv (
    key text NOT NULL,
    value text
);


--
-- Name: oauth2_jtis; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth2_jtis (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    jti text NOT NULL,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: oauth2_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth2_sessions (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    kind text NOT NULL,
    key text NOT NULL,
    request_id text NOT NULL,
    access_token_signature text DEFAULT ''::text NOT NULL,
    active boolean DEFAULT true NOT NULL,
    request_data jsonb NOT NULL,
    expires_at timestamp with time zone,
    client_id text NOT NULL,
    CONSTRAINT chk_oauth2_sessions_client_id CHECK ((client_id = (request_data ->> 'client_id'::text)))
);


--
-- Name: oidc_clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_clients (
    id text NOT NULL,
    created_at timestamp with time zone,
    name character varying(255),
    callback_urls jsonb,
    image_type character varying(10),
    created_by_id uuid,
    is_public boolean DEFAULT false,
    pkce_enabled boolean DEFAULT false,
    logout_callback_urls jsonb,
    credentials jsonb,
    launch_url text,
    requires_reauthentication boolean DEFAULT false NOT NULL,
    dark_image_type text,
    is_group_restricted boolean DEFAULT false NOT NULL,
    requires_pushed_authorization_requests boolean DEFAULT false NOT NULL,
    skip_consent boolean DEFAULT false NOT NULL,
    pkce_supported boolean DEFAULT false NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    client_type text DEFAULT 'standard'::text NOT NULL,
    metadata_expires_at timestamp with time zone,
    metadata_grant_types jsonb,
    access_token_duration_minutes bigint DEFAULT 60 NOT NULL,
    refresh_token_duration_minutes bigint DEFAULT 43200 NOT NULL
);


--
-- Name: oidc_clients_allowed_api_permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_clients_allowed_api_permissions (
    oidc_client_id text NOT NULL,
    api_permission_id uuid NOT NULL,
    subject_type text NOT NULL,
    CONSTRAINT oidc_clients_allowed_api_permissions_subject_type_check CHECK ((subject_type = ANY (ARRAY['user'::text, 'client'::text])))
);


--
-- Name: oidc_clients_allowed_apis; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_clients_allowed_apis (
    oidc_client_id text NOT NULL,
    api_id uuid NOT NULL,
    subject_type text NOT NULL,
    CONSTRAINT oidc_clients_allowed_apis_subject_type_check CHECK ((subject_type = ANY (ARRAY['user'::text, 'client'::text])))
);


--
-- Name: oidc_clients_allowed_user_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oidc_clients_allowed_user_groups (
    user_group_id uuid NOT NULL,
    oidc_client_id text NOT NULL
);


--
-- Name: reauthentication_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reauthentication_tokens (
    id text NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    token text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    user_id uuid NOT NULL
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    version bigint NOT NULL,
    dirty boolean NOT NULL
);


--
-- Name: scim_service_providers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.scim_service_providers (
    id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL,
    endpoint text NOT NULL,
    token text NOT NULL,
    last_synced_at timestamp with time zone,
    oidc_client_id text NOT NULL
);


--
-- Name: storage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.storage (
    path text NOT NULL,
    data bytea NOT NULL,
    size bigint NOT NULL,
    mod_time timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: user_authorized_oidc_clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_authorized_oidc_clients (
    scope jsonb DEFAULT '[]'::jsonb NOT NULL,
    user_id uuid NOT NULL,
    client_id text NOT NULL,
    last_used_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);


--
-- Name: user_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_groups (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    friendly_name character varying(255) NOT NULL,
    name character varying(255) NOT NULL,
    ldap_id text,
    updated_at timestamp with time zone
);


--
-- Name: user_groups_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_groups_users (
    user_id uuid NOT NULL,
    user_group_id uuid NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    username public.citext NOT NULL COLLATE pg_catalog."C",
    email character varying(255),
    first_name character varying(100),
    last_name character varying(100),
    is_admin boolean DEFAULT false NOT NULL,
    ldap_id text,
    locale text,
    disabled boolean DEFAULT false NOT NULL,
    display_name text NOT NULL,
    updated_at timestamp with time zone,
    email_verified boolean DEFAULT false NOT NULL
);


--
-- Name: webauthn_credentials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webauthn_credentials (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    name character varying(255) NOT NULL,
    credential_id bytea NOT NULL,
    public_key bytea NOT NULL,
    attestation_type character varying(20) NOT NULL,
    transport jsonb NOT NULL,
    user_id uuid,
    backup_eligible boolean DEFAULT false NOT NULL,
    backup_state boolean DEFAULT false NOT NULL
);


--
-- Name: webauthn_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webauthn_sessions (
    id uuid NOT NULL,
    created_at timestamp with time zone,
    challenge character varying(255) NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    user_verification character varying(255) NOT NULL,
    credential_params jsonb DEFAULT '[]'::jsonb NOT NULL
);


--
-- Name: api_keys api_keys_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_key_key UNIQUE (key);


--
-- Name: api_keys api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (id);


--
-- Name: api_permissions api_permissions_api_id_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_permissions
    ADD CONSTRAINT api_permissions_api_id_key_key UNIQUE (api_id, key);


--
-- Name: api_permissions api_permissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_permissions
    ADD CONSTRAINT api_permissions_pkey PRIMARY KEY (id);


--
-- Name: apis apis_audience_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apis
    ADD CONSTRAINT apis_audience_key UNIQUE (audience);


--
-- Name: apis apis_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apis
    ADD CONSTRAINT apis_pkey PRIMARY KEY (id);


--
-- Name: audit_logs audit_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_logs
    ADD CONSTRAINT audit_logs_pkey PRIMARY KEY (id);


--
-- Name: custom_claims custom_claims_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_claims
    ADD CONSTRAINT custom_claims_pkey PRIMARY KEY (id);


--
-- Name: custom_claims custom_claims_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_claims
    ADD CONSTRAINT custom_claims_unique UNIQUE (key, user_id, user_group_id);


--
-- Name: francis_active_actors francis_active_actors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_active_actors
    ADD CONSTRAINT francis_active_actors_pkey PRIMARY KEY (actor_type, actor_id);


--
-- Name: francis_actor_state francis_actor_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_actor_state
    ADD CONSTRAINT francis_actor_state_pkey PRIMARY KEY (actor_type, actor_id);


--
-- Name: francis_alarms francis_alarms_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_alarms
    ADD CONSTRAINT francis_alarms_pkey PRIMARY KEY (alarm_id);


--
-- Name: francis_cluster_config francis_cluster_config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_cluster_config
    ADD CONSTRAINT francis_cluster_config_pkey PRIMARY KEY (cluster_config_id);


--
-- Name: francis_consumed_join_tokens francis_consumed_join_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_consumed_join_tokens
    ADD CONSTRAINT francis_consumed_join_tokens_pkey PRIMARY KEY (join_token);


--
-- Name: francis_dead_jobs francis_dead_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_dead_jobs
    ADD CONSTRAINT francis_dead_jobs_pkey PRIMARY KEY (job_id);


--
-- Name: francis_host_actor_types francis_host_actor_types_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_host_actor_types
    ADD CONSTRAINT francis_host_actor_types_pkey PRIMARY KEY (host_id, actor_type);


--
-- Name: francis_hosts francis_hosts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_hosts
    ADD CONSTRAINT francis_hosts_pkey PRIMARY KEY (host_id);


--
-- Name: francis_metadata francis_metadata_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_metadata
    ADD CONSTRAINT francis_metadata_pkey PRIMARY KEY (key);


--
-- Name: interaction_sessions interaction_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interaction_sessions
    ADD CONSTRAINT interaction_sessions_pkey PRIMARY KEY (id);


--
-- Name: kv kv_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kv
    ADD CONSTRAINT kv_pkey PRIMARY KEY (key);


--
-- Name: oauth2_jtis oauth2_jtis_jti_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth2_jtis
    ADD CONSTRAINT oauth2_jtis_jti_key UNIQUE (jti);


--
-- Name: oauth2_jtis oauth2_jtis_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth2_jtis
    ADD CONSTRAINT oauth2_jtis_pkey PRIMARY KEY (id);


--
-- Name: oauth2_sessions oauth2_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth2_sessions
    ADD CONSTRAINT oauth2_sessions_pkey PRIMARY KEY (id);


--
-- Name: oidc_clients_allowed_api_permissions oidc_clients_allowed_api_permissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_api_permissions
    ADD CONSTRAINT oidc_clients_allowed_api_permissions_pkey PRIMARY KEY (oidc_client_id, api_permission_id, subject_type);


--
-- Name: oidc_clients_allowed_apis oidc_clients_allowed_apis_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_apis
    ADD CONSTRAINT oidc_clients_allowed_apis_pkey PRIMARY KEY (oidc_client_id, api_id, subject_type);


--
-- Name: oidc_clients_allowed_user_groups oidc_clients_allowed_user_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_user_groups
    ADD CONSTRAINT oidc_clients_allowed_user_groups_pkey PRIMARY KEY (oidc_client_id, user_group_id);


--
-- Name: oidc_clients oidc_clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients
    ADD CONSTRAINT oidc_clients_pkey PRIMARY KEY (id);


--
-- Name: reauthentication_tokens reauthentication_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reauthentication_tokens
    ADD CONSTRAINT reauthentication_tokens_pkey PRIMARY KEY (id);


--
-- Name: reauthentication_tokens reauthentication_tokens_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reauthentication_tokens
    ADD CONSTRAINT reauthentication_tokens_token_key UNIQUE (token);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: scim_service_providers scim_service_providers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scim_service_providers
    ADD CONSTRAINT scim_service_providers_pkey PRIMARY KEY (id);


--
-- Name: storage storage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storage
    ADD CONSTRAINT storage_pkey PRIMARY KEY (path);


--
-- Name: user_authorized_oidc_clients user_authorized_oidc_clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_authorized_oidc_clients
    ADD CONSTRAINT user_authorized_oidc_clients_pkey PRIMARY KEY (user_id, client_id);


--
-- Name: user_groups user_groups_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups
    ADD CONSTRAINT user_groups_name_key UNIQUE (name);


--
-- Name: user_groups user_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups
    ADD CONSTRAINT user_groups_pkey PRIMARY KEY (id);


--
-- Name: user_groups_users user_groups_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups_users
    ADD CONSTRAINT user_groups_users_pkey PRIMARY KEY (user_id, user_group_id);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: webauthn_credentials webauthn_credentials_credential_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webauthn_credentials
    ADD CONSTRAINT webauthn_credentials_credential_id_key UNIQUE (credential_id);


--
-- Name: webauthn_credentials webauthn_credentials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webauthn_credentials
    ADD CONSTRAINT webauthn_credentials_pkey PRIMARY KEY (id);


--
-- Name: webauthn_sessions webauthn_sessions_challenge_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webauthn_sessions
    ADD CONSTRAINT webauthn_sessions_challenge_key UNIQUE (challenge);


--
-- Name: webauthn_sessions webauthn_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webauthn_sessions
    ADD CONSTRAINT webauthn_sessions_pkey PRIMARY KEY (id);


--
-- Name: francis_active_actors_host_scan_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_active_actors_host_scan_idx ON public.francis_active_actors USING btree (host_id, actor_type);


--
-- Name: francis_actor_state_expiration_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_actor_state_expiration_time_idx ON public.francis_actor_state USING btree (actor_state_expiration_time) WHERE (actor_state_expiration_time IS NOT NULL);


--
-- Name: francis_actor_type_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_actor_type_idx ON public.francis_host_actor_types USING btree (actor_type);


--
-- Name: francis_alarm_due_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_alarm_due_time_idx ON public.francis_alarms USING btree (alarm_due_time);


--
-- Name: francis_alarm_lease_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX francis_alarm_lease_id_idx ON public.francis_alarms USING btree (alarm_lease_id) WHERE (alarm_lease_id IS NOT NULL);


--
-- Name: francis_alarm_ref_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX francis_alarm_ref_idx ON public.francis_alarms USING btree (actor_type, actor_id, alarm_name);


--
-- Name: francis_dead_jobs_actor_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_dead_jobs_actor_idx ON public.francis_dead_jobs USING btree (actor_type, actor_id);


--
-- Name: francis_host_address_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX francis_host_address_idx ON public.francis_hosts USING btree (host_address);


--
-- Name: francis_host_last_health_check_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX francis_host_last_health_check_idx ON public.francis_hosts USING btree (host_last_health_check);


--
-- Name: idx_api_keys_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_keys_expires_at ON public.api_keys USING btree (expires_at);


--
-- Name: idx_api_permissions_api_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_permissions_api_id ON public.api_permissions USING btree (api_id);


--
-- Name: idx_audit_logs_client_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_client_name ON public.audit_logs USING btree (((data ->> 'clientName'::text)));


--
-- Name: idx_audit_logs_country; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_country ON public.audit_logs USING btree (country);


--
-- Name: idx_audit_logs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_created_at ON public.audit_logs USING btree (created_at);


--
-- Name: idx_audit_logs_event; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_event ON public.audit_logs USING btree (event);


--
-- Name: idx_audit_logs_user_agent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_user_agent ON public.audit_logs USING btree (user_agent);


--
-- Name: idx_audit_logs_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_logs_user_id ON public.audit_logs USING btree (user_id);


--
-- Name: idx_interaction_sessions_client_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_interaction_sessions_client_id ON public.interaction_sessions USING btree (client_id);


--
-- Name: idx_interaction_sessions_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_interaction_sessions_user_id ON public.interaction_sessions USING btree (user_id);


--
-- Name: idx_oauth2_jtis_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_oauth2_jtis_expires_at ON public.oauth2_jtis USING btree (expires_at);


--
-- Name: idx_oauth2_sessions_client_subject; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_oauth2_sessions_client_subject ON public.oauth2_sessions USING btree (client_id, ((request_data #>> '{session,subject}'::text[])), kind, active);


--
-- Name: idx_oauth2_sessions_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_oauth2_sessions_expires_at ON public.oauth2_sessions USING btree (expires_at);


--
-- Name: idx_oauth2_sessions_kind_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_oauth2_sessions_kind_key ON public.oauth2_sessions USING btree (kind, key);


--
-- Name: idx_oauth2_sessions_kind_request; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_oauth2_sessions_kind_request ON public.oauth2_sessions USING btree (kind, request_id);


--
-- Name: idx_oidc_clients_allowed_apis_api_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_oidc_clients_allowed_apis_api_id ON public.oidc_clients_allowed_apis USING btree (api_id);


--
-- Name: idx_reauthentication_tokens_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reauthentication_tokens_expires_at ON public.reauthentication_tokens USING btree (expires_at);


--
-- Name: idx_webauthn_sessions_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webauthn_sessions_expires_at ON public.webauthn_sessions USING btree (expires_at);


--
-- Name: user_groups_ldap_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX user_groups_ldap_id ON public.user_groups USING btree (ldap_id);


--
-- Name: users_ldap_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX users_ldap_id ON public.users USING btree (ldap_id);


--
-- Name: francis_active_actors francis_active_actors_delete_update_alarms; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER francis_active_actors_delete_update_alarms AFTER DELETE ON public.francis_active_actors REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION public.francis_active_actors_delete_update_alarms_fn();


--
-- Name: api_keys api_keys_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: api_permissions api_permissions_api_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_permissions
    ADD CONSTRAINT api_permissions_api_id_fkey FOREIGN KEY (api_id) REFERENCES public.apis(id) ON DELETE CASCADE;


--
-- Name: audit_logs audit_logs_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_logs
    ADD CONSTRAINT audit_logs_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: custom_claims custom_claims_user_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_claims
    ADD CONSTRAINT custom_claims_user_group_id_fkey FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE;


--
-- Name: custom_claims custom_claims_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.custom_claims
    ADD CONSTRAINT custom_claims_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth2_sessions fk_oauth2_sessions_client_id; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth2_sessions
    ADD CONSTRAINT fk_oauth2_sessions_client_id FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: francis_active_actors francis_active_actors_host_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_active_actors
    ADD CONSTRAINT francis_active_actors_host_id_fkey FOREIGN KEY (host_id) REFERENCES public.francis_hosts(host_id) ON DELETE CASCADE;


--
-- Name: francis_consumed_join_tokens francis_consumed_join_tokens_host_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_consumed_join_tokens
    ADD CONSTRAINT francis_consumed_join_tokens_host_id_fkey FOREIGN KEY (host_id) REFERENCES public.francis_hosts(host_id) ON DELETE CASCADE;


--
-- Name: francis_host_actor_types francis_host_actor_types_host_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.francis_host_actor_types
    ADD CONSTRAINT francis_host_actor_types_host_id_fkey FOREIGN KEY (host_id) REFERENCES public.francis_hosts(host_id) ON DELETE CASCADE;


--
-- Name: interaction_sessions interaction_sessions_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interaction_sessions
    ADD CONSTRAINT interaction_sessions_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: interaction_sessions interaction_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interaction_sessions
    ADD CONSTRAINT interaction_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_api_permissions oidc_clients_allowed_api_permissions_api_permission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_api_permissions
    ADD CONSTRAINT oidc_clients_allowed_api_permissions_api_permission_id_fkey FOREIGN KEY (api_permission_id) REFERENCES public.api_permissions(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_api_permissions oidc_clients_allowed_api_permissions_oidc_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_api_permissions
    ADD CONSTRAINT oidc_clients_allowed_api_permissions_oidc_client_id_fkey FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_apis oidc_clients_allowed_apis_api_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_apis
    ADD CONSTRAINT oidc_clients_allowed_apis_api_id_fkey FOREIGN KEY (api_id) REFERENCES public.apis(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_apis oidc_clients_allowed_apis_oidc_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_apis
    ADD CONSTRAINT oidc_clients_allowed_apis_oidc_client_id_fkey FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_user_groups oidc_clients_allowed_user_groups_oidc_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_user_groups
    ADD CONSTRAINT oidc_clients_allowed_user_groups_oidc_client_id_fkey FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: oidc_clients_allowed_user_groups oidc_clients_allowed_user_groups_user_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients_allowed_user_groups
    ADD CONSTRAINT oidc_clients_allowed_user_groups_user_group_id_fkey FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE;


--
-- Name: oidc_clients oidc_clients_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oidc_clients
    ADD CONSTRAINT oidc_clients_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: reauthentication_tokens reauthentication_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reauthentication_tokens
    ADD CONSTRAINT reauthentication_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: scim_service_providers scim_service_providers_oidc_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scim_service_providers
    ADD CONSTRAINT scim_service_providers_oidc_client_id_fkey FOREIGN KEY (oidc_client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: user_authorized_oidc_clients user_authorized_oidc_clients_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_authorized_oidc_clients
    ADD CONSTRAINT user_authorized_oidc_clients_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.oidc_clients(id) ON DELETE CASCADE;


--
-- Name: user_authorized_oidc_clients user_authorized_oidc_clients_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_authorized_oidc_clients
    ADD CONSTRAINT user_authorized_oidc_clients_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: user_groups_users user_groups_users_user_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups_users
    ADD CONSTRAINT user_groups_users_user_group_id_fkey FOREIGN KEY (user_group_id) REFERENCES public.user_groups(id) ON DELETE CASCADE;


--
-- Name: user_groups_users user_groups_users_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_groups_users
    ADD CONSTRAINT user_groups_users_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: webauthn_credentials webauthn_credentials_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webauthn_credentials
    ADD CONSTRAINT webauthn_credentials_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

--
-- End of reference dump (upstream cut-off: v2.14.0 / migration 20260814120000).
--

\unrestrict bbssT8JLdvbGp4WtA6DUmc8Nwbx0LFSNRchjlVwuNSZiCOj6DuccPYaa1lwAlwp
