"""Durable jobs. Processing and result writes are one database transaction."""
import json
import logging
import time
from datetime import datetime, timedelta, timezone

from sqlalchemy import delete, select, update

from . import compat
from .db import Asset, Audit, Batch, Decision, Entry, ImportItem, Job, Release, Review, Revision, Session, Workspace, init_db, now
from .services import admins, current_revisions, export_baseline, notify, published_revision_ids, workspace_lock

log = logging.getLogger('finesub-review.worker')


def import_batch(db, batch):
    db.execute(delete(ImportItem).where(ImportItem.batch_id == batch.id))
    existing = {r.path: r for r in current_revisions(db)}
    documents = {p: r.content for p, r in existing.items() if r.operation != 'delete'}
    warnings, candidates, seen = [], [], set()
    assets = db.scalars(select(Asset).where(Asset.batch_id == batch.id)).all()
    unpacked = [file for asset in assets for file in compat.unpack(asset.filename, asset.data)]
    if sum(len(data) for _, data in unpacked if data is not None) > compat.MAX_TOTAL:
        raise ValueError('批次解压后总量超过 30 MB')
    source_files, notices = compat.select_sources(unpacked)
    warnings.extend(notices)
    profile = db.get(Workspace, 1).profile
    total = 0
    for filename, data in source_files:
        if data is None:
            warnings.append(f'未识别文件，原件已保留：{filename}')
            continue
        total += len(data)
        if total > compat.MAX_TOTAL:
            raise ValueError('批次解压后总量超过 30 MB')
        try:
            text = compat.text_decode(data)
            if filename.lower().endswith('.jsonl'):
                candidates.extend(compat.proposals(text, documents, reserved_paths=existing, profile=profile))
            else:
                path = compat.knowledge_path(filename)
                parts, notices = compat.split_markdown(path, text)
                warnings.extend(f'{filename}：{note}' for note in notices)
                candidates.extend({'path': p, 'content': c, 'operation': 'upsert'} for p, c in parts)
        except ValueError as exc:
            candidates.append({'path': filename, 'content': '', 'operation': 'upsert', 'error': str(exc), 'grouped': filename.lower().endswith('.jsonl')})
    if len(candidates) > compat.MAX_FILES:
        raise ValueError('解析后超过 2000 个审核项')
    for item in candidates:
        path, content = item['path'], item['content']
        origin = item.get('origin_path', path)
        base = existing.get(origin)
        errors = [item['error']] if item.get('error') else []
        item_warnings = []
        if path.casefold() in seen:
            errors.append('同一批次出现重复目标路径，请拆分批次或合并内容')
        seen.add(path.casefold())
        if not errors and item['operation'] != 'delete':
            item_warnings, errors = compat.validate(path, content, profile,
                                                   baseline=base.content if base and base.path == path and base.operation != 'delete' else None)
        classification = 'invalid' if errors else 'new' if not base else 'duplicate' if base.digest == compat.digest(content) and base.operation == item['operation'] and origin == path else 'changed'
        if not errors and origin != path:
            classification = 'renamed'
        if not errors and item['operation'] == 'delete':
            classification = 'deleted'
        if base and base.status == 'pending' and classification not in ('duplicate', 'invalid'):
            item_warnings.append('将生成新的待审版本，旧审核记录仍然保留')
        db.add(ImportItem(batch_id=batch.id, path=path, origin_path=origin, content=content,
                          base_revision=base.id if base else None, operation=item['operation'],
                          grouped=item.get('grouped', False), classification=classification,
                          warnings=item_warnings, error='；'.join(errors)))
    batch.status, batch.warnings = 'preview', warnings
    notify(db, [batch.author_id], f'「{batch.name}」预检完成，共 {len(candidates)} 个审核项')


def publish(db, release):
    ws = workspace_lock(db)
    if ws.latest_release != release.parent_id:
        raise ValueError('发布基线已经变化，请重新创建发布')
    if not release.rollback_of:
        satisfied = set(release.changes) | published_revision_ids(db)
        for rid in release.changes:
            rev = db.get(Revision, rid)
            if not rev or rev.status != 'approved' or db.get(Entry, rev.entry_id).current_revision != rid:
                raise ValueError('选中的内容已修订或裁决已变化，请重新创建发布任务')
            if rev.group_id:
                related = db.scalars(select(Revision.id).join(Entry, Entry.current_revision == Revision.id).where(Revision.group_id == rev.group_id)).all()
                if not set(related).issubset(satisfied):
                    raise ValueError('关联变更组在排队期间已变化，请重新审核并创建发布任务')
    revisions = [db.get(Revision, rid) for rid in release.snapshot.values()]
    if any(r is None for r in revisions):
        raise ValueError('发布快照中存在无效版本')
    parent_release = db.get(Release, release.parent_id) if release.parent_id else None
    baselines = {r.path: export_baseline(db, r, parent_release) for r in revisions}
    files, report = compat.build_files({r.path: r.content for r in revisions}, release.profile,
                                       kind_hints={r.path: r.kind for r in revisions}, baselines=baselines)
    release.report = report
    if report['errors']:
        raise ValueError('；'.join(report['errors'][:20]))
    manifest = {'id': release.id, 'name': release.name, 'parent_id': release.parent_id,
                'rollback_of': release.rollback_of, 'profile': release.profile,
                'created_at': release.created_at, 'notes': release.notes,
                'files': {p: compat.digest(c) for p, c in files.items()}, 'snapshot': release.snapshot, 'changes': release.changes, 'validation': report}
    revision_ids = list(set(release.snapshot.values()) | set(release.changes))
    records = db.scalars(select(Review).where(Review.revision_id.in_(revision_ids))).all() if revision_ids else []
    decisions = db.scalars(select(Decision).where(Decision.revision_id.in_(revision_ids))).all() if revision_ids else []
    record_data = [{k: getattr(r, k) for k in ('id', 'revision_id', 'author_id', 'verdict', 'body', 'quote', 'suggestion', 'evidence', 'resolution', 'resolution_note', 'created_at')} for r in records]
    decision_data = [{k: getattr(d, k) for k in ('revision_id', 'author_id', 'status', 'reason', 'created_at')} for d in decisions]
    release.artifact = compat.make_zip(files)
    release.archive = compat.make_zip({**{f'knowledge/{p}': c for p, c in files.items()},
                                      'manifest.json': json.dumps(manifest, ensure_ascii=False, indent=2),
                                      'reviews.json': json.dumps(record_data, ensure_ascii=False, indent=2),
                                      'decisions.json': json.dumps(decision_data, ensure_ascii=False, indent=2)})
    release.status = 'ready'
    ws.latest_release = release.id
    for entry in db.scalars(select(Entry)).all():
        if release.rollback_of or entry.id in release.snapshot:
            entry.published_revision = release.snapshot.get(entry.id)
    for rid in release.changes:
        revision = db.get(Revision, rid)
        db.get(Entry, revision.entry_id).published_revision = rid
    db.add(Audit(actor_id=release.author_id, action='release.complete', target_id=release.id,
                 detail={'files': len(files), 'sha256': compat.digest(release.artifact)}))
    notify(db, admins(db), f'「{release.name}」已发布，可下载知识库包')


def run_once(job_id=None):
    cutoff = (datetime.now(timezone.utc) - timedelta(minutes=10)).isoformat()
    with Session.begin() as db:
        db.execute(update(Job).where(Job.status == 'processing', Job.touched_at < cutoff).values(status='queued'))
        if job_id:
            job_id = db.scalar(select(Job.id).where(Job.id == job_id, Job.status == 'queued'))
        else:
            job_id = db.scalar(select(Job.id).where(Job.status == 'queued').order_by(Job.created_at).limit(1))
        if not job_id:
            return False
        claimed = db.execute(update(Job).where(Job.id == job_id, Job.status == 'queued').values(status='processing', touched_at=now(), attempts=Job.attempts + 1)).rowcount
        if not claimed:
            # Another worker won the claim; this call did no work. Callers that
            # name a job must poll its status instead of assuming it ran.
            return False
        claimed_job = db.get(Job, job_id)
        target = db.get(Batch if claimed_job.kind == 'import' else Release, claimed_job.target_id)
        if target:
            target.status = 'processing'
    try:
        with Session.begin() as db:
            job = db.get(Job, job_id)
            # Hold a row write lock so another worker cannot reclaim active work.
            db.execute(update(Job).where(Job.id == job.id).values(touched_at=now()))
            if job.kind == 'import':
                import_batch(db, db.get(Batch, job.target_id))
            elif job.kind == 'release':
                publish(db, db.get(Release, job.target_id))
            else:
                raise ValueError('未知后台任务')
            job.status, job.error, job.touched_at = 'done', '', now()
    except Exception as exc:
        log.exception('Job %s failed', job_id)
        with Session.begin() as db:
            job = db.get(Job, job_id)
            job.status, job.error, job.touched_at = 'failed', str(exc)[:5000], now()
            model = Batch if job.kind == 'import' else Release
            target = db.get(model, job.target_id)
            if target:
                target.status = 'failed'
                if isinstance(target, Release):
                    target.report = {'errors': [str(exc)], 'warnings': []}
                notify(db, [target.author_id], f'后台任务失败：{str(exc)[:250]}')
    return True


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO)
    init_db()
    while True:
        if not run_once():
            time.sleep(2)
