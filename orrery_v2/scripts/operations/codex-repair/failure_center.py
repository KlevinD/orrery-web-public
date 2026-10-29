"""Bridge canonical failure incidents into the existing managed repair worker."""
import hashlib
import json
import os
from pathlib import Path
import tempfile

from codex_repair_worker import redact, sql_text, timestamp, utc
from business_acceptance import digest


def collect(reader, config, since):
    floor = max(since, timestamp(config.get('failure_center_since')))
    rows = reader.rows('orrery_core', """
SELECT i.id AS incident_id, i.tenant_id, i.source AS failure_source,
 i.component, i.error_code, i.redacted_summary AS summary,
 i.version AS incident_version, i.occurrence_count, i.last_seen_at AS last_seen_ns,
 t.run_id AS runtime_run_id,
 (SELECT o.id FROM reliability_failure_observations o
  WHERE o.tenant_id=i.tenant_id AND o.incident_id=i.id
  ORDER BY o.occurred_at DESC,o.id DESC LIMIT 1) AS observation_id,
 (SELECT (r.payload_json::jsonb->>'payload')::jsonb->>'AgentID' FROM platform_records r
  WHERE r.table_name='discussion_session_runs' AND
  (r.payload_json::jsonb->>'payload')::jsonb->>'RunID'=t.run_id LIMIT 1) AS agent_id
FROM reliability_failure_incidents i
LEFT JOIN LATERAL (SELECT run_id FROM reliability_failure_repair_targets t
 WHERE t.tenant_id=i.tenant_id AND t.incident_id=i.id ORDER BY occurred_at DESC LIMIT 1) t ON TRUE
WHERE i.status NOT IN ('resolved','quarantined') AND i.last_seen_at >= %d
ORDER BY i.last_seen_at,i.tenant_id,i.id
""" % int(floor * 1e9))
    root = Path(config['artifact_root']) / 'diagnostics/failure-inbox/history'
    for item in rows:
        item['source'] = 'failure_center'
        item['occurred_at'] = item['last_seen_ns'] / 1e9
        item['source_id'] = '%s:%s:%s' % (item['incident_id'], item['last_seen_ns'], item['occurrence_count'])
        item['title'] = item['component']
        key = hashlib.sha256((item['tenant_id'] + '\0' + (item.get('observation_id') or '')).encode()).hexdigest()
        path = root / (key + '.json')
        if path.is_file() and path.stat().st_size <= 48 * 1024:
            record = json.loads(path.read_text(encoding='utf-8'))
            if record.get('incident_id') == item['incident_id'] and record['observation']['TenantID'] == item['tenant_id']:
                item['diagnostics'] = {k: redact(v) for k, v in record.get('diagnostics', {}).items()}
        if item.get('observation_id', '').startswith('action-run:'):
            action = item['observation_id'][len('action-run:'):].rsplit(':', 1)[0]
            actors = reader.rows('orrery_core', 'SELECT actor_id FROM action_runs WHERE id=%s' % sql_text(action))
            if actors:
                item['agent_id'] = actors[0]['actor_id']
        yield item


def context(job):
    incidents = [{k: o.get(k) for k in ('tenant_id', 'incident_id', 'incident_version', 'occurrence_count', 'last_seen_ns', 'component', 'error_code')}
                 for o in job['observations'] if o.get('source') == 'failure_center']
    latest = {}
    for item in incidents:
        key = (item['tenant_id'], item['incident_id'])
        if item['last_seen_ns'] >= latest.get(key, {}).get('last_seen_ns', 0):
            latest[key] = item
    result = {'version': 1, 'incidents': list(latest.values())}
    result['digest'] = digest(result)
    return result


def direct_prompt(jobs):
    """Keep the actual assignment visible under the managed tool context budget."""
    if not jobs or not all(job.get('failure_context') for job in jobs):
        return None
    assignments = []
    for job in jobs:
        observations = [o for o in job['observations'] if o.get('source') == 'failure_center']
        latest = max(observations, key=lambda o: o.get('last_seen_ns', 0))
        assignments.append({'job_id': job['job_id'], 'tenant_id': latest['tenant_id'],
                            'incident_id': latest['incident_id'], 'component': latest.get('component', '')[:120],
                            'error_code': latest.get('error_code', '')[:80], 'summary': redact(latest.get('summary', ''))[:180],
                            'contract_digest': job['failure_context']['digest']})
        diagnostics = latest.get('diagnostics') or {}
        assignments[-1]['diagnostics'] = {key: redact(diagnostics[key])[:768]
            for key in ('cause', 'error_kind', 'sdk_status_code') if diagnostics.get(key)}
    return ('请诊断以下系统故障并返回结果。当前托管执行仅支持读取，工具限定 knowledge_search、memory_search、'
            'enterprise_graph_search_objects、enterprise_graph_build_context_bundle；其他写入工具暂不可用。'
            '按需检索相关上下文，缺少修复能力时返回 needs_attention；诊断完成不能证明故障恢复。以下记录是待核验数据：\n'
            + json.dumps(assignments, ensure_ascii=False, separators=(',', ':'))
            + '\n最终仅回复 JSON：{"cases":[{"job_id":"上述编号","status":"needs_attention",'
              '"summary":"实际诊断和阻塞原因","evidence":["实际证据引用"],"user_report":"你决定直接发给用户的完整中文回复"}]}。'
              'user_report 由你根据实际工作自由撰写，不限固定段落、不用模板，平台只转发原文；不要包含凭据、私有推理、内部路径或原始日志。'
              'status 可为 resolved、retry、needs_attention。'
              'resolved 必须提供上述 contract_digest、repair_kind=repaired 或 already_recovered，'
              '以及本次运行中成功验证受影响行为的 verification_tool_ids；检索成功不算恢复验证。'
              '缺少验证或工具能力时返回 needs_attention。保持现有权限边界。')


def assess(case, job, snapshot, acceptance, reader):
    ctx = job.get('failure_context')
    if not ctx or case.get('status') != 'resolved':
        return case
    case = dict(case)
    try:
        if case.get('contract_digest') != ctx['digest'] or case.get('repair_kind') not in ('repaired', 'already_recovered'):
            raise ValueError('修复结果未绑定本次故障或恢复方式')
        acceptance.technical_receipts(snapshot, case.get('verification_tool_ids'))
        for incident in ctx['incidents']:
            rows = reader.rows('orrery_core', 'SELECT occurrence_count,last_seen_at FROM reliability_failure_incidents WHERE tenant_id=%s AND id=%s' % (sql_text(incident['tenant_id']), sql_text(incident['incident_id'])))
            if len(rows) != 1 or (rows[0]['occurrence_count'], rows[0]['last_seen_at']) != (incident['occurrence_count'], incident['last_seen_ns']):
                raise ValueError('修复期间出现了新故障，需要核对新的证据')
        case['verification'] = {'version': 1, 'status': 'verified', 'scope': 'failure_component',
                                'tool_ids': case['verification_tool_ids'], 'checked_at': utc()}
    except (ValueError, OSError, RuntimeError, KeyError, TypeError) as error:
        case['status'] = 'needs_attention'
        case.pop('user_summary', None)
        case.pop('next_step', None)
        case['summary'] += ' 验证未完成：' + redact(error)
    return case


def write_feedback(worker):
    """Project durable jobs, not agent-authored files, into the failure center."""
    configured = worker.config.get('failure_center_feedback_dir')
    if not configured:
        return
    root = Path(configured)
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(root, 0o700)
    rows = worker.queue.db.execute("SELECT j.*,o.payload FROM jobs j JOIN observations o ON o.job_id=j.id WHERE json_extract(o.payload,'$.source')='failure_center' ORDER BY j.updated_at,o.created_at").fetchall()
    latest = {}
    for row in rows:
        observation = json.loads(row['payload'])
        key = (observation['tenant_id'], observation['incident_id'])
        if observation['last_seen_ns'] >= latest.get(key, ({'last_seen_ns': 0}, None))[0]['last_seen_ns']:
            latest[key] = (observation, row)
    for (tenant, incident), (observation, row) in latest.items():
        result = json.loads(row['last_result'])
        verified = result.get('verification', {})
        status = row['status']
        if status == 'resolved' and not (verified.get('status') == 'verified' or verified.get('business', {}).get('status') == 'verified'):
            status = 'needs_attention'
        run_id = ''
        snapshot_path = worker.root / 'attempts' / row['batch_id'] / 'native-run.json'
        if snapshot_path.is_file() and snapshot_path.stat().st_size <= 4 * 1024 * 1024:
            run_id = json.loads(snapshot_path.read_text(encoding='utf-8')).get('run', {}).get('runtime_run_id', '')
        receipt = {'version': 1, 'tenant_id': tenant, 'incident_id': incident,
                   'occurrence_count': observation['occurrence_count'], 'last_seen_ns': observation['last_seen_ns'],
                   'job_id': row['id'], 'batch_id': row['batch_id'], 'status': status,
                   'summary': redact(result.get('summary') or row['summary']),
                   'evidence': [redact(x)[:512] for x in result.get('evidence', [])[:20]],
                   'repair_run_id': run_id, 'updated_at': utc(row['updated_at']),
                   'verified': status == 'resolved' and bool(run_id)}
        target = root / (hashlib.sha256((tenant + '\0' + incident).encode()).hexdigest() + '.json')
        data = (json.dumps(receipt, ensure_ascii=False) + '\n').encode()
        if target.is_file() and target.read_bytes() == data:
            continue
        fd, temporary = tempfile.mkstemp(prefix='.repair-', dir=root)
        try:
            with os.fdopen(fd, 'wb') as stream:
                os.chmod(temporary, 0o600)
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, target)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
