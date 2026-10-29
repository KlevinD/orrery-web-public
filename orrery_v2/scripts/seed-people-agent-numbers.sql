\set ON_ERROR_STOP on

BEGIN;

CREATE TEMP TABLE people_agent_number_seed (
    subject_id text PRIMARY KEY,
    agent_number text NOT NULL UNIQUE
) ON COMMIT DROP;

INSERT INTO people_agent_number_seed (subject_id, agent_number) VALUES
    ('member-openclaw-main', 'A000001'),
    ('member-openclaw-bianbian', 'A000002'),
    ('member-openclaw-dafu', 'A000003'),
    ('member-openclaw-fafa', 'A000004'),
    ('member-openclaw-jordan-belfort', 'A000005'),
    ('member-openclaw-marlon-brando', 'A000006'),
    ('member-openclaw-tangdou', 'A000007'),
    ('member-openclaw-tiedan', 'A000008'),
    ('member-openclaw-xiaodoubao', 'A000009'),
    ('subject-U7ELXDTY5SHHWYIKMR35JJ5B7A', 'A000010'),
    ('subject-PHV33EPZE73735PF4SHROVUQ7V', 'A000011'),
    ('subject-ZPN5WG26CFOFL7SNGKMQDAL22F', 'A000012'),
    ('subject-UAFX57CPG5SVFKSCLNAHGMDXJA', 'A000013'),
    ('subject-JTECX4KOUME7YJA6FF64OQOSA2', 'A000014'),
    ('subject-ZICTRCCFQQMMMSYAAAX5FVXAT3', 'A000015'),
    ('subject-INQM5TUHGCNAP2RZ7UIZY6BRVB', 'A000016'),
    ('subject-MQTHEWSHVY3PZCFXSD6QTWOEPR', 'A000017'),
    ('subject-3WY5PT5HNOWOX22BCPGMM4G67B', 'A000018');

CREATE TEMP TABLE people_agent_number_backup (schema_name text NOT NULL);
INSERT INTO people_agent_number_backup
VALUES ('migration_backup_people_agent_numbers_' || to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYYMMDD"T"HH24MISS"Z"'));

DO $$
DECLARE
    backup_schema text;
BEGIN
    SELECT schema_name INTO backup_schema FROM people_agent_number_backup;
    EXECUTE format('CREATE SCHEMA %I', backup_schema);
    EXECUTE format('CREATE TABLE %I.people_agent_number_seed AS TABLE people_agent_number_seed', backup_schema);
    EXECUTE format(
        'CREATE TABLE %1$I.people_subject_fact AS SELECT root.* FROM public.people_subject_fact root JOIN people_agent_number_seed seed ON root.tenant_id = %2$L AND root.subject_id = seed.subject_id AND root.fact_code = %3$L',
        backup_schema, 'tenant-preview', 'agent_number'
    );
    EXECUTE format(
        'CREATE TABLE %1$I.people_subject_fact_revision AS SELECT revision.* FROM public.people_subject_fact_revision revision JOIN public.people_subject_fact root ON root.tenant_id = revision.tenant_id AND root.subject_fact_id = revision.subject_fact_id JOIN people_agent_number_seed seed ON root.tenant_id = %2$L AND root.subject_id = seed.subject_id AND root.fact_code = %3$L',
        backup_schema, 'tenant-preview', 'agent_number'
    );
END;
$$;

DO $$
DECLARE
    invalid_count bigint;
BEGIN
    SELECT count(*) INTO invalid_count
    FROM people_agent_number_seed seed
    LEFT JOIN people_subject subject
      ON subject.tenant_id = 'tenant-preview'
     AND subject.subject_id = seed.subject_id
     AND subject.subject_type = 'digital'
    WHERE subject.subject_id IS NULL;
    IF invalid_count > 0 THEN
        RAISE EXCEPTION 'agent number seed contains % missing or non-digital People subjects', invalid_count;
    END IF;
END;
$$;

INSERT INTO people_subject_fact (
    tenant_id, subject_fact_id, subject_id, fact_code, current_revision, created_at, created_by
)
SELECT
    'tenant-preview', 'subject-fact-agent-number-' || lower(seed.agent_number),
    seed.subject_id, 'agent_number', 1, now(), 'system:agent-number-migration'
FROM people_agent_number_seed seed
ON CONFLICT (tenant_id, subject_fact_id) DO NOTHING;

INSERT INTO people_subject_fact_revision (
    tenant_id, subject_fact_id, revision, value, verification_status, evidence_ref,
    valid_from, valid_to, recorded_at, superseded_at, changed_by, source_system_id,
    change_set_id, correlation_id
)
SELECT
    'tenant-preview', 'subject-fact-agent-number-' || lower(seed.agent_number), 1,
    to_jsonb(seed.agent_number), 'verified', 'migration:agent-profile-ids-20260825',
    now(), NULL, now(), NULL, 'system:agent-number-migration', 'orrery-legacy',
    NULL, 'agent-number-migration-20260825'
FROM people_agent_number_seed seed
ON CONFLICT (tenant_id, subject_fact_id, revision) DO NOTHING;

INSERT INTO people_audit_event (
    tenant_id, audit_event_id, actor_type, actor_id, action, aggregate_type,
    aggregate_id, aggregate_revision, reason, before_data, after_data,
    occurred_at, correlation_id, causation_id
)
SELECT
    'tenant-preview', 'audit-agent-number-' || lower(seed.agent_number), 'service',
    'system:agent-number-migration', 'subject_fact.asserted', 'subject_fact',
    'subject-fact-agent-number-' || lower(seed.agent_number), 1,
    'Assign canonical number to an existing digital workforce subject', NULL,
    jsonb_build_object(
        'subject_id', seed.subject_id,
        'code', 'agent_number',
        'value', seed.agent_number,
        'verification_status', 'verified',
        'source_system_id', 'orrery-legacy'
    ), now(), 'agent-number-migration-20260825', NULL
FROM people_agent_number_seed seed
ON CONFLICT (tenant_id, audit_event_id) DO NOTHING;

INSERT INTO people_outbox_event (
    tenant_id, event_id, event_type, schema_version, aggregate_type, aggregate_id,
    aggregate_revision, operation, payload, occurred_at, correlation_id, causation_id
)
SELECT
    'tenant-preview', 'event-agent-number-' || lower(seed.agent_number),
    'people.subject_fact.v2.changed', 2, 'subject_fact',
    'subject-fact-agent-number-' || lower(seed.agent_number), 1, 'asserted',
    jsonb_build_object(
        'id', 'subject-fact-agent-number-' || lower(seed.agent_number),
        'subject_id', seed.subject_id,
        'code', 'agent_number',
        'value', seed.agent_number,
        'verification_status', 'verified',
        'source_system_id', 'orrery-legacy',
        'revision', 1
    ), now(), 'agent-number-migration-20260825', NULL
FROM people_agent_number_seed seed
ON CONFLICT (tenant_id, event_id) DO NOTHING;

SELECT schema_name AS backup_schema FROM people_agent_number_backup;
SELECT root.subject_id, revision.value #>> '{}' AS agent_number
FROM people_subject_fact root
JOIN people_subject_fact_revision revision
  ON revision.tenant_id = root.tenant_id
 AND revision.subject_fact_id = root.subject_fact_id
 AND revision.revision = root.current_revision
WHERE root.tenant_id = 'tenant-preview'
  AND root.fact_code = 'agent_number'
ORDER BY agent_number;

COMMIT;
