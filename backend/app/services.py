import json
import re
from collections import Counter

from fastapi import HTTPException
from sqlalchemy import select, update

from . import compat
from .db import Asset, Audit, Batch, Decision, Entry, ImportItem, Job, Notice, Release, Review, Revision, User, Workspace


def require(db, model, key):
    obj = db.get(model, key)
    if not obj:
        raise HTTPException(404, '记录不存在')
    return obj


def workspace_lock(db):
    # A database write lock serializes content changes on both supported engines.
    db.execute(update(Workspace).where(Workspace.id == 1).values(serial=Workspace.serial + 1))
    db.flush()
    workspace = db.get(Workspace, 1)
    db.refresh(workspace)
    return workspace


def audit(db, user, action, target='', **detail):
    db.add(Audit(actor_id=user.id if user else None, action=action, target_id=target, detail=detail))


def notify(db, recipients, text, entry_id=None, exclude=None):
    for user_id in set(recipients) - {exclude, None}:
        db.add(Notice(user_id=user_id, text=text, entry_id=entry_id))


def admins(db):
    return db.scalars(select(User.id).where(User.active.is_(True), User.role == 'admin')).all()


def snapshot(db):
    ws = db.get(Workspace, 1)
    released = db.get(Release, ws.latest_release) if ws.latest_release else None
    return dict(released.snapshot) if released else {}


def current_revisions(db):
    return db.scalars(select(Revision).join(Entry, Entry.current_revision == Revision.id)).all()


def published_revision_ids(db):
    # Published deletions are absent from snapshots but still satisfy their group.
    return set(db.scalars(select(Entry.published_revision).where(Entry.published_revision.is_not(None))).all())


def revision_view(revision):
    return {key: getattr(revision, key) for key in ('id', 'entry_id', 'parent_id', 'base_published', 'batch_id', 'number', 'path', 'group_id', 'title', 'category', 'kind', 'aliases', 'summary', 'source', 'operation', 'status', 'author_id', 'created_at', 'digest')}


def compatibility_baseline(db, revision):
    """An already stored revision is the trust boundary, never upload metadata."""
    if revision is None or revision.operation == 'delete':
        return None
    previous = db.get(Revision, revision.parent_id) if revision.parent_id else revision
    return previous.content if previous and previous.path == revision.path and previous.operation != 'delete' else None


def export_baseline(db, revision, parent_release):
    """Native delta is based on the prior release, or the initial stored source."""
    if parent_release and revision.entry_id in parent_release.snapshot:
        base = db.get(Revision, parent_release.snapshot[revision.entry_id])
    else:
        base = db.scalar(select(Revision).where(Revision.entry_id == revision.entry_id).order_by(Revision.number).limit(1))
    return base.content if base and base.path == revision.path and base.operation != 'delete' else None


def new_revision(db, entry, content, author, source='', batch_id=None, operation='upsert', path=None, group_id=None, kind_hint=''):
    previous = db.get(Revision, entry.current_revision) if entry.current_revision else None
    path = path or entry.path
    meta = compat.metadata(path, content)
    if not meta['kind']:
        meta['kind'] = kind_hint or (previous.kind if previous and compat.is_node_markdown(content) else '')
    revision = Revision(entry_id=entry.id, parent_id=previous.id if previous else None,
                        base_published=snapshot(db).get(entry.id), number=previous.number + 1 if previous else 1,
                        content=content, digest=compat.digest(content), author_id=author.id, source=source,
                        path=path, batch_id=batch_id, operation=operation, group_id=group_id,
                        **meta)
    db.add(revision)
    db.flush()
    entry.current_revision, entry.path = revision.id, path
    reviewers = db.scalars(select(Review.author_id).join(Revision, Revision.id == Review.revision_id).where(Revision.entry_id == entry.id)).all()
    notify(db, [*reviewers, entry.assignee_id], f'「{revision.title}」已更新至 v{revision.number}，可提交新的审核记录', entry.id, author.id)
    return revision


def confirm_batch(db, batch_id, item_ids, user):
    workspace_lock(db)
    batch = require(db, Batch, batch_id)
    if batch.status == 'confirmed':
        return {'status': 'confirmed', 'created': 0}
    if batch.status != 'preview':
        raise HTTPException(409, '导入预检尚未完成')
    items = db.scalars(select(ImportItem).where(ImportItem.batch_id == batch.id)).all()
    chosen = [item for item in items if item.id in set(item_ids)]
    if not chosen or len(chosen) != len(set(item_ids)):
        raise HTTPException(422, '请选择有效的导入项')
    grouped = {item.id for item in items if item.grouped and not item.error and item.classification != 'duplicate'}
    if any(item.grouped for item in chosen) and not grouped.issubset(set(item_ids)):
        raise HTTPException(409, 'JSONL 关联变更必须一起导入')
    if any(item.grouped and item.error for item in items) and any(item.grouped for item in chosen):
        raise HTTPException(409, 'JSONL 变更组存在错误，请修正后重新上传')
    assets = db.scalars(select(Asset).where(Asset.batch_id == batch.id)).all()
    files, _ = compat.select_sources([file for asset in assets for file in compat.unpack(asset.filename, asset.data)])
    kind_hints = compat.index_types(files)
    count = 0
    for item in chosen:
        if item.error or item.classification in ('invalid', 'duplicate'):
            raise HTTPException(422, '不能导入错误或重复项')
        if item.operation != 'delete':
            base = db.get(Revision, item.base_revision) if item.base_revision else None
            _, errors = compat.validate(item.path, item.content, db.get(Workspace, 1).profile,
                                         baseline=base.content if base and base.path == item.path and base.operation != 'delete' else None)
            if errors:
                raise HTTPException(422, '当前兼容规则校验失败：' + '；'.join(errors))
        entry = db.scalar(select(Entry).where(Entry.path == (item.origin_path or item.path)))
        if (entry.current_revision if entry else None) != item.base_revision:
            raise HTTPException(409, f'{item.path} 在预检后发生变化，请重新上传预检')
        collision = db.scalar(select(Entry).where(Entry.path == item.path))
        if collision and (not entry or collision.id != entry.id):
            raise HTTPException(409, f'目标文件名已被占用：{item.path}')
        if not entry:
            entry = Entry(path=item.path)
            db.add(entry)
            db.flush()
        new_revision(db, entry, item.content, user, batch.source, batch.id, item.operation, item.path, batch.id if item.grouped else None,
                     kind_hint=kind_hints.get(item.path, ''))
        count += 1
    batch.status = 'confirmed'
    audit(db, user, 'import.confirm', batch.id, items=count)
    return {'status': batch.status, 'created': count}


def decide(db, entry_id, revision_id, status, reason, user):
    workspace_lock(db)
    entry = require(db, Entry, entry_id)
    if entry.current_revision != revision_id:
        raise HTTPException(409, '内容版本已经变化，请刷新后重新裁决')
    revision = require(db, Revision, revision_id)
    if entry.published_revision == revision.id:
        raise HTTPException(409, '该版本已经发布；需要调整时请创建新修订')
    if status == 'approved' and revision.operation != 'delete':
        _, errors = compat.validate(revision.path, revision.content, db.get(Workspace, 1).profile,
                                     baseline=compatibility_baseline(db, revision))
        if errors:
            raise HTTPException(422, '内容校验失败：' + '；'.join(errors))
    revision.status = status
    db.add(Decision(revision_id=revision.id, author_id=user.id, status=status, reason=reason))
    recipients = db.scalars(select(Review.author_id).where(Review.revision_id == revision.id)).all()
    labels = {'approved': '通过', 'rejected': '驳回', 'needs_changes': '待补充', 'pending': '重新审核'}
    notify(db, [*recipients, entry.assignee_id], f'管理员将「{revision.title}」决定为：{labels[status]}', entry.id, user.id)
    audit(db, user, 'review.decide', revision.id, status=status, reason=reason[:500])
    return revision_view(revision)


def publication_candidates(db):
    """Explain existing publication constraints before the user starts a job."""
    revisions = current_revisions(db)
    entries = {e.id: e for e in db.scalars(select(Entry)).all()}
    base = snapshot(db)
    published = published_revision_ids(db)
    profile = db.get(Workspace, 1).profile
    candidates = [r for r in revisions if r.status == 'approved' and entries[r.entry_id].published_revision != r.id]
    blockers = {}
    for rev in candidates:
        reasons = []
        if base.get(rev.entry_id) != rev.base_published:
            reasons.append('正式版本已变化，需重新修订和审核')
        if rev.operation != 'delete':
            _, errors = compat.validate(rev.path, rev.content, profile, baseline=compatibility_baseline(db, rev))
            reasons.extend(errors)
        blockers[rev.id] = reasons
    result = []
    for rev in candidates:
        reasons = list(blockers[rev.id])
        related = [r for r in revisions if rev.group_id and r.group_id == rev.group_id and r.id not in published]
        for other in related:
            if other.status != 'approved':
                reasons.append(f'关联词条「{other.title}」尚未通过')
            elif other.id != rev.id and blockers.get(other.id):
                reasons.append(f'关联词条「{other.title}」需先解决发布问题')
        result.append({**revision_view(rev), 'blockers': reasons, 'published': False,
                       'related': [revision_view(r) for r in related if r.id != rev.id]})
    return result


def prepare_release(db, user, name, notes, revision_ids=None, rollback_id=None):
    ws = workspace_lock(db)
    if db.scalar(select(Release.id).where(Release.status.in_(['queued', 'processing']))):
        raise HTTPException(409, '已有发布任务正在执行，请稍后再试')
    base = snapshot(db)
    if rollback_id:
        old = require(db, Release, rollback_id)
        if old.status != 'ready':
            raise HTTPException(409, '只能回退到成功的历史发布版本')
        target = dict(old.snapshot)
    else:
        selected = set(revision_ids or [])
        if not selected:
            raise HTTPException(422, '请选择通过裁决且未发布的版本')
        target = dict(base)
        published = published_revision_ids(db)
        revisions = db.scalars(select(Revision).where(Revision.id.in_(selected))).all()
        if len(revisions) != len(selected):
            raise HTTPException(422, '包含无效版本')
        for rev in revisions:
            entry = db.get(Entry, rev.entry_id)
            if rev.status != 'approved' or entry.current_revision != rev.id:
                raise HTTPException(409, f'「{rev.title}」不是最新的已通过版本')
            if entry.published_revision == rev.id:
                raise HTTPException(409, f'「{rev.title}」已经发布')
            if base.get(entry.id) != rev.base_published:
                raise HTTPException(409, f'「{rev.title}」的发布基线已变化，请重新修订和审核')
            if rev.group_id:
                related = db.scalars(select(Revision.id).join(Entry, Entry.current_revision == Revision.id).where(Revision.group_id == rev.group_id)).all()
                if not set(related).issubset(selected | published):
                    raise HTTPException(409, '关联变更组必须全部通过并一起发布')
            if rev.operation == 'delete':
                target.pop(entry.id, None)
            else:
                target[entry.id] = rev.id
    paths = [db.get(Revision, rid).path.casefold() for rid in target.values()]
    if len(paths) != len(set(paths)):
        raise HTTPException(409, '发布版本存在文件名冲突，请先解决重命名冲突')
    release = Release(name=name, notes=notes, author_id=user.id, parent_id=ws.latest_release,
                      rollback_of=rollback_id, profile=ws.profile, snapshot=target, changes=list(revision_ids or []))
    db.add(release)
    db.flush()
    db.add(Job(kind='release', target_id=release.id))
    audit(db, user, 'release.request', release.id, count=len(target), rollback_of=rollback_id)
    return release
