\set ON_ERROR_STOP on

BEGIN;
SET LOCAL lock_timeout = '15s';
SET LOCAL statement_timeout = '20min';

CREATE TEMP TABLE agent_profile_id_constraint_state (
    table_name regclass NOT NULL,
    constraint_name text NOT NULL,
    was_deferrable boolean NOT NULL,
    was_deferred boolean NOT NULL
) ON COMMIT DROP;

INSERT INTO agent_profile_id_constraint_state
SELECT conrelid::regclass, conname, condeferrable, condeferred
FROM pg_constraint
WHERE contype = 'f'
  AND connamespace = 'public'::regnamespace;

DO $$
DECLARE
    constraint_record record;
BEGIN
    FOR constraint_record IN SELECT * FROM agent_profile_id_constraint_state LOOP
        EXECUTE format(
            'ALTER TABLE %s ALTER CONSTRAINT %I DEFERRABLE INITIALLY DEFERRED',
            constraint_record.table_name,
            constraint_record.constraint_name
        );
    END LOOP;
END;
$$;
SET CONSTRAINTS ALL DEFERRED;

CREATE TEMP TABLE agent_profile_id_map (
    old_id text PRIMARY KEY,
    new_id text NOT NULL UNIQUE
) ON COMMIT DROP;

INSERT INTO agent_profile_id_map (old_id, new_id) VALUES
    ('gangbeng', 'A000001'),
    ('bianbian', 'A000002'),
    ('dafu', 'A000003'),
    ('fafa', 'A000004'),
    ('jordan-belfort', 'A000005'),
    ('marlon-brando', 'A000006'),
    ('tangdou', 'A000007'),
    ('tiedan', 'A000008'),
    ('xiaodoubao', 'A000009'),
    ('lead-agent', 'A000010'),
    ('coordinator-agent', 'A000011'),
    ('qa-plan-agent', 'A000012'),
    ('qa-check-agent', 'A000013'),
    ('website-receptionist-public', 'A000014'),
    ('website-receptionist-customer', 'A000015'),
    ('personal-assistant-c3aa4571a63b1e0824c489b9e0012a73d8c64acd9ad9fcca3b10f5df7cdba774', 'A000016'),
    ('personal-assistant-4beda162a0a4e970566e6d3b76192b6ce0ebdc66d8a0c0547b2a75f969b0ddbf', 'A000017'),
    ('personal-assistant-80d6dc82de3db67606be007aeb57d154e3d3f68d01bc249937d0530932d2939e', 'A000018');

CREATE OR REPLACE FUNCTION pg_temp.canonical_agent_profile_ids(value text)
RETURNS text
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    mapping record;
    migrated text := value;
BEGIN
    FOR mapping IN
        SELECT old_id, new_id
        FROM agent_profile_id_map
        ORDER BY length(old_id) DESC, old_id
    LOOP
        migrated := regexp_replace(
            migrated,
            '(^|[^[:alnum:]-])' || mapping.old_id || '([^[:alnum:]-]|$)',
            E'\\1' || mapping.new_id || E'\\2',
            'g'
        );
    END LOOP;
    RETURN migrated;
END;
$$;

CREATE OR REPLACE FUNCTION pg_temp.has_legacy_agent_profile_id(value text)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT value ~ '(^|[^[:alnum:]-])(gangbeng|bianbian|dafu|fafa|jordan-belfort|marlon-brando|tangdou|tiedan|xiaodoubao|lead-agent|coordinator-agent|qa-plan-agent|qa-check-agent|website-receptionist-public|website-receptionist-customer|personal-assistant-c3aa4571a63b1e0824c489b9e0012a73d8c64acd9ad9fcca3b10f5df7cdba774|personal-assistant-4beda162a0a4e970566e6d3b76192b6ce0ebdc66d8a0c0547b2a75f969b0ddbf|personal-assistant-80d6dc82de3db67606be007aeb57d154e3d3f68d01bc249937d0530932d2939e)([^[:alnum:]-]|$)';
$$;

CREATE TEMP TABLE agent_profile_id_backup (schema_name text NOT NULL);
INSERT INTO agent_profile_id_backup
VALUES ('migration_backup_agent_ids_' || to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYYMMDD"T"HH24MISS"Z"'));

DO $$
DECLARE
    backup_schema text;
    table_record record;
    where_clause text;
    copied bigint;
BEGIN
    SELECT schema_name INTO backup_schema FROM agent_profile_id_backup;
    EXECUTE format('CREATE SCHEMA %I', backup_schema);
    EXECUTE format(
        'CREATE TABLE %I.agent_profile_id_map AS TABLE agent_profile_id_map',
        backup_schema
    );
    FOR table_record IN
        SELECT c.table_schema, c.table_name,
               string_agg(
                   format(
                       '(%1$I IS NOT NULL AND pg_temp.has_legacy_agent_profile_id(%1$I::text))',
                       c.column_name
                   ),
                   ' OR ' ORDER BY c.ordinal_position
               ) AS predicate
        FROM information_schema.columns c
        JOIN information_schema.tables t
          ON t.table_schema = c.table_schema
         AND t.table_name = c.table_name
        WHERE c.table_schema = 'public'
          AND t.table_type = 'BASE TABLE'
          AND c.data_type IN ('text', 'character varying', 'character', 'json', 'jsonb')
        GROUP BY c.table_schema, c.table_name
        ORDER BY c.table_name
    LOOP
        where_clause := table_record.predicate;
        EXECUTE format(
            'CREATE TABLE %1$I.%2$I AS SELECT * FROM %3$I.%2$I WHERE %4$s',
            backup_schema,
            table_record.table_name,
            table_record.table_schema,
            where_clause
        );
        GET DIAGNOSTICS copied = ROW_COUNT;
        IF copied = 0 THEN
            EXECUTE format('DROP TABLE %I.%I', backup_schema, table_record.table_name);
        END IF;
    END LOOP;
END;
$$;

CREATE TEMP TABLE agent_profile_id_migration_stats (
    table_name text NOT NULL,
    column_name text NOT NULL,
    rows_changed bigint NOT NULL
) ON COMMIT DROP;

DO $$
DECLARE
    column_record record;
    changed bigint;
    expression text;
BEGIN
    FOR column_record IN
        SELECT c.table_schema, c.table_name, c.column_name, c.data_type
        FROM information_schema.columns c
        JOIN information_schema.tables t
          ON t.table_schema = c.table_schema
         AND t.table_name = c.table_name
        WHERE c.table_schema = 'public'
          AND t.table_type = 'BASE TABLE'
          AND c.is_generated = 'NEVER'
          AND c.data_type IN ('text', 'character varying', 'character', 'json', 'jsonb')
        ORDER BY c.table_name, c.ordinal_position
    LOOP
        expression := CASE column_record.data_type
            WHEN 'jsonb' THEN format('pg_temp.canonical_agent_profile_ids(%1$I::text)::jsonb', column_record.column_name)
            WHEN 'json' THEN format('pg_temp.canonical_agent_profile_ids(%1$I::text)::json', column_record.column_name)
            ELSE format('pg_temp.canonical_agent_profile_ids(%1$I::text)', column_record.column_name)
        END;
        EXECUTE format(
            'UPDATE %1$I.%2$I SET %3$I = %4$s WHERE %3$I IS NOT NULL AND pg_temp.has_legacy_agent_profile_id(%3$I::text)',
            column_record.table_schema,
            column_record.table_name,
            column_record.column_name,
            expression
        );
        GET DIAGNOSTICS changed = ROW_COUNT;
        IF changed > 0 THEN
            INSERT INTO agent_profile_id_migration_stats VALUES (
                column_record.table_name,
                column_record.column_name,
                changed
            );
        END IF;
    END LOOP;
END;
$$;

DO $$
DECLARE
    column_record record;
    remaining bigint;
BEGIN
    FOR column_record IN
        SELECT c.table_schema, c.table_name, c.column_name
        FROM information_schema.columns c
        JOIN information_schema.tables t
          ON t.table_schema = c.table_schema
         AND t.table_name = c.table_name
        WHERE c.table_schema = 'public'
          AND t.table_type = 'BASE TABLE'
          AND c.data_type IN ('text', 'character varying', 'character', 'json', 'jsonb')
    LOOP
        EXECUTE format(
            'SELECT count(*) FROM %1$I.%2$I WHERE %3$I IS NOT NULL AND pg_temp.has_legacy_agent_profile_id(%3$I::text)',
            column_record.table_schema,
            column_record.table_name,
            column_record.column_name
        ) INTO remaining;
        IF remaining > 0 THEN
            RAISE EXCEPTION 'agent profile ID migration left % rows in %.%', remaining, column_record.table_name, column_record.column_name;
        END IF;
    END LOOP;
END;
$$;

SET CONSTRAINTS ALL IMMEDIATE;

DO $$
DECLARE
    constraint_record record;
    clause text;
BEGIN
    FOR constraint_record IN SELECT * FROM agent_profile_id_constraint_state LOOP
        clause := CASE
            WHEN NOT constraint_record.was_deferrable THEN 'NOT DEFERRABLE'
            WHEN constraint_record.was_deferred THEN 'DEFERRABLE INITIALLY DEFERRED'
            ELSE 'DEFERRABLE INITIALLY IMMEDIATE'
        END;
        EXECUTE format(
            'ALTER TABLE %s ALTER CONSTRAINT %I %s',
            constraint_record.table_name,
            constraint_record.constraint_name,
            clause
        );
    END LOOP;
END;
$$;

SELECT table_name, column_name, rows_changed
FROM agent_profile_id_migration_stats
ORDER BY table_name, column_name;

SELECT schema_name AS backup_schema FROM agent_profile_id_backup;

COMMIT;
