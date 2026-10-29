"""Bind repairs to the original scheduled intent and read platform acceptance evidence.

This module only reads application records. It never executes an agent-supplied
verification command, accepts a business approval, or replays a scheduled run.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess

from codex_repair_worker import sql_text, timestamp, utc


def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def safe_text(value):
    value = str(value or '')
    value = re.sub(r'(?i)(bearer\s+)[^\s,;"\']+', r'\1[REDACTED]', value)
    value = re.sub(r'''(?i)((?:api[_-]?key|secret(?:[_-]?key)?|password|access[_-]?token|refresh[_-]?token|authorization)["']?\s*[=:]\s*["']?)[^\s,;"']+''', r'\1[REDACTED]', value)
    return re.sub(r'(://)[^/@\s]+:[^/@\s]+@', r'\1[REDACTED]@', value)


def presentation(value):
    if isinstance(value, str):
        return safe_text(value)
    if isinstance(value, list):
        return [presentation(item) for item in value]
    if isinstance(value, dict):
        return {key: presentation(item) for key, item in value.items()}
    return value


CONTRACT_FIELDS = ('Goal', 'Scope', 'AcceptanceCriteria', 'ExpectedDeliverables',
                   'EvidenceRequirements', 'Constraints', 'ExecutionPermissionScopes',
                   'RequireExplicitToolScopes', 'QAEnabled', 'QAPlanRequired', 'QACheckRequired')


class BusinessAcceptance:
    def __init__(self, reader, artifact_root):
        self.reader, self.artifact_root = reader, Path(artifact_root).resolve()

    def record(self, table, key):
        if not key:
            return {}
        rows = self.reader.rows('orrery_core', "SELECT (payload_json::jsonb->>'payload')::jsonb AS p FROM public.platform_records WHERE table_name=%s AND record_key=%s" % (sql_text(table), sql_text(key)))
        return rows[0]['p'] if rows else {}

    def related(self, table, field, key):
        # Table and field are code constants, never observation/agent input.
        rows = self.reader.rows('orrery_core', "SELECT (payload_json::jsonb->>'payload')::jsonb AS p FROM public.platform_records WHERE table_name=%s AND payload_json::jsonb->>%s=%s" % (sql_text(table), sql_text(field), sql_text(key)))
        return [row['p'] for row in rows]

    def capture(self, job):
        observations = job.get('observations') or []
        sources = {(o.get('tenant_id'), o.get('task_id'), o.get('source_id')) for o in observations if o.get('source') == 'scheduled'}
        context = {'version': 1, 'captured_at': utc(), 'available': False, 'missing': []}
        try:
            if len(sources) != 1:
                raise ValueError('原始任务执行无法唯一定位')
            tenant, task_id, run_id = next(iter(sources))
            if not all((tenant, task_id, run_id)) or any(o.get('tenant_id') != tenant for o in observations):
                raise ValueError('原始任务租户或执行归属不一致')
            run = self.record('scheduled_task_runs', run_id)
            context.update(self.from_run(run, tenant, task_id, run_id))
        except (OSError, ValueError, RuntimeError, TypeError, KeyError, subprocess.SubprocessError):
            context['missing'] = ['原始任务合同不可用，不能推断业务已完成']
        context['digest'] = digest(context)
        return context

    def from_run(self, run, tenant, task_id, run_id):
        if (run.get('tenant_id'), run.get('scheduled_task_id'), run.get('scheduled_run_id')) != (tenant, task_id, run_id):
            raise ValueError('原始执行归属不一致')
        snapshot = run.get('configuration_snapshot') or {}
        if (snapshot.get('tenant_id'), snapshot.get('scheduled_task_id')) != (tenant, task_id):
            raise ValueError('缺少原执行配置快照')
        target = snapshot.get('target') or {}
        goal = target.get('task_goal') or target.get('prompt') or target.get('message') or ''
        contract = {}
        if run.get('task_id'):
            record = self.record('mission_task_records', run['task_id'])
            raw = record.get('Contract') or {}
            if raw.get('ID') != run['task_id'] or raw.get('MissionID') != run.get('mission_id') or raw.get('Goal') != goal:
                raise ValueError('责任合同与原业务目标不一致')
            contract = {field: raw.get(field) for field in CONTRACT_FIELDS}
        intent = {'description': snapshot.get('description'), 'goal': goal,
                  'instructions': target.get('prompt') or target.get('message') or '',
                  'target_mode': target.get('mode'), 'tool_id': target.get('tool_id'),
                  'tool_arguments_digest': digest(target.get('tool_arguments')),
                  'agent_id': target.get('agent_id') or target.get('lead_agent_id'),
                  'result_contract': run.get('result_contract') or target.get('result_contract'),
                  'delivery': run.get('delivery') or snapshot.get('delivery'),
                  'allowed_tools': (snapshot.get('execution') or {}).get('allowed_tool_ids'),
                  'policy': snapshot.get('policy'),
                  'contract': contract}
        missing = []
        if not goal:
            missing.append('原任务未提供明确业务目标')
        if not contract.get('AcceptanceCriteria') or not contract.get('ExpectedDeliverables'):
            missing.append('原任务缺少结构化验收条件或交付要求')
        if len(json.dumps(intent, ensure_ascii=False)) > 64000:
            raise ValueError('原业务要求超出读取上限，不能静默截断')
        rendered = presentation(intent)
        if rendered != intent:
            missing.append('业务要求包含已脱敏内容，不能自动确认完整验收')
        return {'available': not missing, 'missing': missing, 'tenant_id': tenant,
                'scheduled_task_id': task_id, 'source_run_id': run_id,
                'source_started_at': run.get('started_at') or run.get('created_at'),
                'source_failed_at': run.get('completed_at') or run.get('started_at') or run.get('created_at'),
                'intent_digest': digest(intent), 'intent': rendered}

    def verify(self, context, candidate_id):
        verdict = {'status': 'pending', 'checked_at': utc(), 'criteria': [], 'reason': ''}
        self._artifact_checks = {}
        try:
            if not context.get('available'):
                raise ValueError('；'.join(context.get('missing') or ['原业务验收条件不完整']))
            candidate_id = candidate_id or context['source_run_id']
            run = self.record('scheduled_task_runs', candidate_id)
            current = self.from_run(run, context['tenant_id'], context['scheduled_task_id'], candidate_id)
            if current['intent_digest'] != context['intent_digest']:
                raise ValueError('验证执行的业务目标、约束或验收条件已变更')
            source_time = timestamp(context['source_started_at'])
            threshold = source_time if candidate_id == context['source_run_id'] else timestamp(context.get('source_failed_at')) or source_time
            if not source_time or timestamp(run.get('started_at')) < threshold:
                raise ValueError('不能用故障之前的成功执行证明恢复')
            if run.get('status') != 'completed' or run.get('failure_reason'):
                raise ValueError('原业务执行尚未成功完成')
            delivery = current['intent'].get('delivery') or {}
            if delivery.get('mode') == 'announce' and (run.get('delivery_status') != 'delivered' or run.get('delivery_error')):
                raise ValueError('原业务要求的交付尚未确认成功')
            task = self.record('mission_task_records', run.get('task_id'))
            contract = task.get('Contract') or {}
            if contract.get('QAPlanRequired') and not contract.get('QAPlanCompleted'):
                raise ValueError('原业务要求的验收计划尚未完成')
            if task.get('Status') not in ('approved', 'completed'):
                raise ValueError('原业务的任务验收尚未通过')
            submission_rows = self.related('mission_task_submissions', 'task_id', run['task_id'])
            submissions = [s for s in submission_rows if s.get('RunID') == run.get('runtime_run_id') and s.get('TaskID') == run['task_id']]
            if len(submissions) != 1 or submissions[0].get('Blocker'):
                raise ValueError('缺少唯一且无阻塞的原业务提交')
            submission = submissions[0]
            if not submission.get('ID') or not submission.get('TaskVersionAccepted'):
                raise ValueError('业务提交未绑定任务版本')
            evidence_rows = self.related('mission_task_evidence_submissions', 'submission_id', submission['ID'])
            deliverable_rows = self.related('mission_task_deliverable_submissions', 'submission_id', submission['ID'])
            evidence = [x['Evidence'] for x in evidence_rows if x.get('TaskID') == run['task_id'] and x.get('SubmissionID') == submission['ID'] and x.get('Evidence', {}).get('ID') in (submission.get('EvidenceRefs') or [])]
            deliverables = [x['Deliverable'] for x in deliverable_rows if x.get('TaskID') == run['task_id'] and x.get('SubmissionID') == submission['ID'] and x.get('Deliverable', {}).get('ID') in (submission.get('DeliverableRefs') or [])]
            evidence = [e for e in evidence if self.traceable(e, run)]
            for criterion in contract.get('AcceptanceCriteria') or []:
                matching = [e['ID'] for e in evidence if e.get('AcceptanceCriterionID') == criterion['ID']]
                verdict['criteria'].append({'id': criterion['ID'], 'description': safe_text(criterion.get('Description')), 'passed': bool(matching), 'evidence_ids': matching})
            if not verdict['criteria'] or not all(c['passed'] for c in verdict['criteria']):
                raise ValueError('原业务验收项缺少实际可追溯证据')
            for requirement in contract.get('EvidenceRequirements') or []:
                if requirement.get('Required') and not any(e.get('EvidenceRequirementID') == requirement['ID'] and e.get('Kind') == requirement.get('Kind') for e in evidence):
                    raise ValueError('原业务的必需证据尚未齐全')
            for expected in contract.get('ExpectedDeliverables') or []:
                if not any(d.get('ExpectedDeliverableID') == expected['ID'] and self.traceable({'Kind': 'artifact', 'ArtifactRef': d.get('ArtifactRef')}, run) for d in deliverables):
                    raise ValueError('原业务的必需产物尚未齐全')
            if contract.get('QACheckRequired'):
                reviews = self.related('mission_task_qa_results', 'task_id', run['task_id'])
                approved = [q for q in reviews if q.get('TaskID') == run['task_id'] and q.get('SubmissionID') == submission['ID'] and q.get('Status') == 'approved' and not q.get('MissingEvidence') and not q.get('RejectionReasons')]
                # Conflicting reviews must be resolved by the canonical workflow.
                if len(approved) != 1 or any(q.get('SubmissionID') == submission['ID'] and q.get('Status') != 'approved' for q in reviews):
                    raise ValueError('原业务质量验收缺失或存在冲突')
                review = approved[0]
                if not all(any(c.get('CriterionID') == criterion['id'] and c.get('Status') == 'passed' for c in review.get('CheckedCriteria') or []) for criterion in verdict['criteria']):
                    raise ValueError('原业务要求的质量验收尚未通过')
                for requirement in contract.get('EvidenceRequirements') or []:
                    if requirement.get('Required') and not any(r.get('RequirementID') == requirement['ID'] and r.get('Status') == 'passed' and r.get('EvidenceID') in [e['ID'] for e in evidence] for r in review.get('EvidenceResults') or []):
                        raise ValueError('必需证据尚未通过质量验收')
                if self.related('mission_task_qa_results', 'task_id', run['task_id']) != reviews:
                    raise ValueError('验收记录发生变化，需要重新核验')
            # Re-read the source of truth after the multi-record verification.
            if self.record('scheduled_task_runs', candidate_id) != run or self.record('mission_task_records', run['task_id']) != task:
                raise ValueError('验收期间任务状态变化，需要重新核验')
            if self.related('mission_task_submissions', 'task_id', run['task_id']) != submission_rows or self.related('mission_task_evidence_submissions', 'submission_id', submission['ID']) != evidence_rows or self.related('mission_task_deliverable_submissions', 'submission_id', submission['ID']) != deliverable_rows:
                raise ValueError('业务提交或证据发生变化，需要重新核验')
            verdict.update(status='verified', reason='已核对原合同、提交、产物完整性及平台验收；业务内容以原验收标准为准', scheduled_run_id=candidate_id,
                           scope='original_execution' if candidate_id == context['source_run_id'] else 'subsequent_execution', submission_id=submission['ID'])
        except (ValueError, KeyError, TypeError) as error:
            verdict['reason'] = safe_text(error)
        except (OSError, RuntimeError, subprocess.SubprocessError):
            verdict['reason'] = '业务验收记录读取失败，保留待验收状态'
        return verdict

    def traceable(self, evidence, run):
        # Reuse only artifacts already attached to the exact business run.
        # External URLs and agent-written prose alone do not prove availability.
        ref = evidence.get('ArtifactRef') or {}
        if evidence.get('Kind') in ('artifact', 'test_log') and ref.get('ID'):
            for artifact in (run.get('result') or {}).get('artifacts') or []:
                if artifact.get('id') != ref['ID']:
                    continue
                key = digest(artifact)
                if key not in self._artifact_checks:
                    if len(self._artifact_checks) >= 32:
                        raise ValueError('产物校验超过单次上限，保留待验收状态')
                    self._artifact_checks[key] = self.artifact_intact(artifact, run)
                if self._artifact_checks[key]:
                    return True
        return False

    def artifact_intact(self, artifact, run):
        # Match the production immutable scheduled-run storage identity, then
        # verify bytes locally. Unsupported/remote stores remain pending.
        size, sha = artifact.get('size_bytes'), artifact.get('sha256') or ''
        if not isinstance(size, int) or not 0 < size <= 64*1024*1024 or not re.fullmatch('[a-f0-9]{64}', sha):
            return False
        hashed = lambda value, length: hashlib.sha256(value.strip().encode()).hexdigest()[:length]
        file_name = artifact.get('file_name') or ''
        if not file_name or '/' in file_name or '\\' in file_name or file_name in ('.', '..'):
            return False
        key = '/'.join(('scheduled-runs', 'v1', hashed(run['tenant_id'], 16), hashed(run['scheduled_run_id'], 32), hashed(artifact['id'], 32), str(size)+'-'+sha, file_name))
        if artifact.get('uri') != 'artifact://' + key:
            return False
        path = (self.artifact_root / key).resolve()
        if not path.is_relative_to(self.artifact_root) or not path.is_file() or path.stat().st_size != size:
            return False
        before, actual = path.stat(), hashlib.sha256()
        with path.open('rb') as stream:
            total = 0
            for chunk in iter(lambda: stream.read(1024*1024), b''):
                total += len(chunk)
                if total > size:
                    return False
                actual.update(chunk)
        after = path.stat()
        # Reading the artifact may update atime. Only identity/content changes
        # invalidate this check; our own access must not reject valid evidence.
        identity = lambda value: (value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns)
        return total == size and identity(after) == identity(before) and actual.hexdigest() == sha

    def latest_candidates(self, context):
        if not context.get('available'):
            return []
        rows = self.reader.rows('orrery_core', "SELECT record_key AS id FROM public.platform_records WHERE table_name='scheduled_task_runs' AND (payload_json::jsonb->>'payload')::jsonb->>'tenant_id'=%s AND (payload_json::jsonb->>'payload')::jsonb->>'scheduled_task_id'=%s AND (payload_json::jsonb->>'payload')::jsonb->>'status'='completed' ORDER BY (payload_json::jsonb->>'payload')::jsonb->>'created_at' DESC LIMIT 3" % (sql_text(context['tenant_id']), sql_text(context['scheduled_task_id'])))
        return [row['id'] for row in rows]

    def technical_receipts(self, snapshot, ids):
        if not isinstance(ids, list) or not ids or len(ids) > 20 or any(not isinstance(i, str) for i in ids):
            raise ValueError('缺少本次技术验证的实际工具回执')
        ref = snapshot.get('trace_ref') or ''
        if not ref.startswith('/uploads/'):
            raise ValueError('技术验证轨迹不可用')
        path = (self.artifact_root / ref[len('/uploads/'):]).resolve()
        if not path.is_relative_to(self.artifact_root) or path.name != 'runtime-trace.json':
            raise ValueError('技术验证轨迹无效')
        if path.stat().st_size > 32*1024*1024:
            raise ValueError('技术验证轨迹超过读取上限')
        trace = json.loads(path.read_text(encoding='utf-8'))
        if trace.get('run_id') != snapshot['run']['runtime_run_id']:
            raise ValueError('技术验证回执不属于本次修复')
        valid, calls = set(), {}
        context_tools = {'enterprise_graph_search_objects', 'enterprise_graph_build_context_bundle', 'knowledge_search', 'memory_search', 'session_search', 'session_read'}
        for ref in trace.get('event_refs') or []:
            if not ref.startswith('/uploads/'):
                continue
            event_path = (self.artifact_root / ref[len('/uploads/'):]).resolve()
            if not event_path.is_relative_to(path.parent) or event_path.stat().st_size > 4*1024*1024:
                raise ValueError('技术验证回执路径无效')
            event = json.loads(event_path.read_text(encoding='utf-8')).get('payload', {}).get('event', {})
            if event.get('kind') == 'tool_call':
                call = event.get('tool_call') or {}
                calls[call.get('tool_use_id')] = (call.get('name') or '').replace('.', '_')
            result = event.get('tool_result') or {}
            name = calls.get(result.get('tool_use_id'))
            if event.get('kind') == 'tool_result' and name and name not in context_tools and not result.get('is_error') and result.get('content'):
                valid.add(result.get('tool_use_id'))
        if not set(ids).issubset(valid):
            raise ValueError('技术验证引用不存在或执行失败')

    def assess(self, case, job, snapshot):
        context = job.get('business_context')
        if not context or case.get('status') != 'resolved':
            return case
        case = dict(case)
        # Never trust a verification object supplied by the repairing agent.
        case.pop('verification', None)
        try:
            if case.get('contract_digest') != context['digest'] or case.get('repair_kind') not in ('repaired', 'already_recovered'):
                raise ValueError('修复结果没有绑定原业务合同或说明恢复方式')
            self.technical_receipts(snapshot, case.get('verification_tool_ids'))
            business = self.verify(context, case.get('business_run_id'))
            case['verification'] = {'version': 1, 'repair_kind': case['repair_kind'], 'business': business,
                                    'business_goal': context.get('intent', {}).get('goal', '')}
            if business['status'] != 'verified':
                case['status'] = 'technical_recovered'
        except (ValueError, OSError, KeyError, TypeError) as error:
            case['status'] = 'needs_attention'
            case.pop('user_summary', None); case.pop('next_step', None)
            case['summary'] += ' 验证未完成：' + safe_text(error)
        return case
