-- Migration: 015_audit_log
-- Append-only, hash-chained audit log for security and privileged actions.
--
-- The table is partitioned by month so retention drops whole partitions. Row
-- updates, deletes and truncates are rejected by triggers. The hash chain is
-- computed by a trigger under an advisory lock, which serializes concurrent
-- writers and keeps prev_hash deterministic. Dropping a partition is only
-- possible through audit_drop_partition(), which first writes a retention
-- audit event and records the last hash of the dropped partition as a chain
-- anchor so verification can continue across retained data.

CREATE TABLE IF NOT EXISTS audit_events (
    id          UUID NOT NULL DEFAULT uuidv7(),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- No foreign keys: evidence must survive deletion of the referenced user or
    -- project, and an ON DELETE action would try to mutate an append-only row.
    actor_id    UUID,
    actor_role  VARCHAR(20) NOT NULL DEFAULT '',
    action      VARCHAR(100) NOT NULL,
    target_type VARCHAR(40) NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    project_id  UUID,
    result      VARCHAR(20) NOT NULL DEFAULT 'success',
    reason      TEXT NOT NULL DEFAULT '',
    ip          INET,
    user_agent  TEXT NOT NULL DEFAULT '',
    request_id  VARCHAR(128) NOT NULL DEFAULT '',
    before_state JSONB,
    after_state  JSONB,
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb,
    prev_hash   TEXT NOT NULL DEFAULT '',
    hash        TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (occurred_at, id),
    CHECK (result IN ('success', 'failure', 'denied'))
) PARTITION BY RANGE (occurred_at);

CREATE TABLE IF NOT EXISTS audit_partitions (
    partition_name TEXT PRIMARY KEY,
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL,
    dropped_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS audit_chain_anchors (
    anchor_hash    TEXT PRIMARY KEY,
    partition_name TEXT NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create the current month plus two years of partitions. The API also calls
-- audit_ensure_partitions() on startup and daily, which extends this horizon.
DO $$
DECLARE
    base  date := date_trunc('month', now())::date;
    i     int;
    pname text;
    pstart TIMESTAMPTZ;
    pend   TIMESTAMPTZ;
BEGIN
    FOR i IN 0..24 LOOP
        pstart := (base + (i || ' months')::interval)::timestamptz;
        pend := (base + ((i + 1) || ' months')::interval)::timestamptz;
        pname := 'audit_events_' || to_char(pstart, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.audit_events FOR VALUES FROM (%L) TO (%L)',
            pname, pstart, pend
        );
        INSERT INTO public.audit_partitions (partition_name, starts_at, ends_at)
        VALUES (pname, pstart, pend)
        ON CONFLICT (partition_name) DO NOTHING;
    END LOOP;
END $$;

-- The trigger owns payload, prev_hash and hash. Callers must not supply them.
CREATE OR REPLACE FUNCTION audit_events_hash_chain() RETURNS trigger AS $$
DECLARE
    previous TEXT;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('spm.audit_events.chain'));

    NEW.occurred_at := COALESCE(NEW.occurred_at, NOW());
    SELECT hash INTO previous
    FROM public.audit_events
    ORDER BY occurred_at DESC, id DESC
    LIMIT 1;

    NEW.prev_hash := COALESCE(previous, '');
    NEW.payload := jsonb_build_object(
        'id', NEW.id,
        'occurred_at', NEW.occurred_at,
        'actor_id', NEW.actor_id,
        'actor_role', NEW.actor_role,
        'action', NEW.action,
        'target_type', NEW.target_type,
        'target_id', NEW.target_id,
        'project_id', NEW.project_id,
        'result', NEW.result,
        'reason', NEW.reason,
        'ip', NEW.ip,
        'user_agent', NEW.user_agent,
        'request_id', NEW.request_id,
        'before', NEW.before_state,
        'after', NEW.after_state
    );
    NEW.hash := encode(
        sha256(convert_to(NEW.prev_hash || '|' || NEW.payload::text, 'UTF8')),
        'hex'
    );
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_hash_chain ON audit_events;
CREATE TRIGGER audit_events_hash_chain
    BEFORE INSERT ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_hash_chain();

CREATE OR REPLACE FUNCTION audit_events_deny_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only' USING ERRCODE = '42501';
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_no_update ON audit_events;
CREATE TRIGGER audit_events_no_update
    BEFORE UPDATE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_deny_mutation();

DROP TRIGGER IF EXISTS audit_events_no_delete ON audit_events;
CREATE TRIGGER audit_events_no_delete
    BEFORE DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_deny_mutation();

DROP TRIGGER IF EXISTS audit_events_no_truncate ON audit_events;
CREATE TRIGGER audit_events_no_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_deny_mutation();

CREATE OR REPLACE FUNCTION audit_ensure_partitions(p_months_ahead INT) RETURNS void AS $$
DECLARE
    base   date := date_trunc('month', now())::date;
    i      int;
    pname  text;
    pstart TIMESTAMPTZ;
    pend   TIMESTAMPTZ;
BEGIN
    IF p_months_ahead < 1 OR p_months_ahead > 60 THEN
        RAISE EXCEPTION 'audit_ensure_partitions: months_ahead must be between 1 and 60';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtext('spm.audit.partitions'));
    FOR i IN 0..p_months_ahead LOOP
        pstart := (base + (i || ' months')::interval)::timestamptz;
        pend := (base + ((i + 1) || ' months')::interval)::timestamptz;
        pname := 'audit_events_' || to_char(pstart, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.audit_events FOR VALUES FROM (%L) TO (%L)',
            pname, pstart, pend
        );
        INSERT INTO public.audit_partitions (partition_name, starts_at, ends_at)
        VALUES (pname, pstart, pend)
        ON CONFLICT (partition_name) DO NOTHING;
    END LOOP;
END $$ LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public;

CREATE OR REPLACE FUNCTION audit_drop_partition(p_name TEXT) RETURNS BOOLEAN AS $$
DECLARE
    v_start  TIMESTAMPTZ;
    v_end    TIMESTAMPTZ;
    v_last   TEXT;
    v_rows   BIGINT;
BEGIN
    IF p_name !~ '^audit_events_[0-9]{6}$' THEN
        RAISE EXCEPTION 'audit_drop_partition: invalid partition name';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('spm.audit_events.chain'));

    SELECT starts_at, ends_at INTO v_start, v_end
    FROM public.audit_partitions
    WHERE partition_name = p_name AND dropped_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN FALSE;
    END IF;

    EXECUTE format('SELECT hash FROM public.%I ORDER BY occurred_at DESC, id DESC LIMIT 1', p_name)
        INTO v_last;
    EXECUTE format('SELECT count(*) FROM public.%I', p_name) INTO v_rows;

    INSERT INTO public.audit_events
        (actor_id, actor_role, action, target_type, target_id, result, reason, after_state)
    VALUES
        (NULL, 'system', 'audit.retention.partition_dropped', 'audit_partition', p_name,
         'success', 'retention',
         jsonb_build_object(
             'partition', p_name,
             'starts_at', v_start,
             'ends_at', v_end,
             'rows', v_rows,
             'last_hash', COALESCE(v_last, '')
         ));

    EXECUTE format('DROP TABLE public.%I', p_name);
    UPDATE public.audit_partitions SET dropped_at = NOW() WHERE partition_name = p_name;
    IF v_last IS NOT NULL THEN
        INSERT INTO public.audit_chain_anchors (anchor_hash, partition_name)
        VALUES (v_last, p_name)
        ON CONFLICT (anchor_hash) DO NOTHING;
    END IF;
    RETURN TRUE;
END $$ LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public;

-- Drop only whole partitions whose retention window has fully elapsed. Each
-- drop writes its own audit event before the partition disappears.
CREATE OR REPLACE FUNCTION audit_prune(p_retention_days INT) RETURNS INT AS $$
DECLARE
    cutoff  TIMESTAMPTZ;
    part    RECORD;
    dropped INT := 0;
BEGIN
    IF p_retention_days < 1 THEN
        RETURN 0;
    END IF;
    cutoff := NOW() - make_interval(days => p_retention_days);
    FOR part IN
        SELECT partition_name
        FROM public.audit_partitions
        WHERE dropped_at IS NULL AND ends_at <= cutoff
        ORDER BY starts_at
    LOOP
        IF public.audit_drop_partition(part.partition_name) THEN
            dropped := dropped + 1;
        END IF;
    END LOOP;
    RETURN dropped;
END $$ LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public;

CREATE INDEX IF NOT EXISTS audit_events_action_idx ON audit_events (action, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_actor_idx ON audit_events (actor_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_project_idx ON audit_events (project_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_result_idx ON audit_events (result, occurred_at DESC);

-- Maintenance functions are not public API. The API role owns them in the
-- supported single-role deployment; a split-role deployment must grant the two
-- maintenance entry points explicitly (see docs/AUDIT.md). Callers must never
-- receive EXECUTE on audit_drop_partition().
REVOKE ALL ON FUNCTION public.audit_ensure_partitions(INT) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.audit_prune(INT) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.audit_drop_partition(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.audit_events_hash_chain() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.audit_events_deny_mutation() FROM PUBLIC;
