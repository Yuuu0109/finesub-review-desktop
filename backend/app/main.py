import difflib
import mimetypes
import os
import re
import secrets
import time
from collections import Counter
from contextlib import asynccontextmanager
from pathlib import Path
from urllib.parse import quote
from typing import Literal

from fastapi import Depends, FastAPI, File, Form, HTTPException, Query, Request, Response, UploadFile
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import and_, delete, func, or_, select, text, update
from sqlalchemy.orm import load_only

from . import compat
from .db import AccessGate, AccessSession, Asset, Audit, Batch, Decision, Entry, ImportItem, Job, LoginSession, Notice, Release, Review, Revision, User, Workspace, init_db, now
from .security import access_session, admin, current_user, db_session, hash_password, token_hash, user_view, verify_password
from .services import admins, audit, compatibility_baseline, confirm_batch, current_revisions, decide, new_revision, notify, prepare_release, publication_candidates, require, revision_view, snapshot, workspace_lock


@asynccontextmanager
async def lifespan(app):
    init_db()
    yield


app = FastAPI(title='FineSub 知识库审核', version='0.3.0', lifespan=lifespan, docs_url=None, redoc_url=None)


def security_headers(response, path):
    response.headers['X-Content-Type-Options'] = 'nosniff'
    response.headers['X-Frame-Options'] = 'DENY'
    response.headers['Referrer-Policy'] = 'same-origin'
    response.headers['Content-Security-Policy'] = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'"
    if path.startswith('/api/'):
        response.headers['Cache-Control'] = 'no-store'
    return response


@app.middleware('http')
async def security_headers_middleware(request, call_next):
    if request.method in ('POST', 'PUT', 'PATCH'):
        length = request.headers.get('content-length')
        if length is None:
            return security_headers(Response('Content-Length required', status_code=411), request.url.path)
        if not length.isdigit() or int(length) > (32 * 1024 * 1024 if request.url.path == '/api/imports' else 3 * 1024 * 1024):
            return security_headers(Response('Request body too large', status_code=413), request.url.path)
    return security_headers(await call_next(request), request.url.path)


class Payload(BaseModel):
    model_config = ConfigDict(extra='forbid', str_strip_whitespace=True)


class Login(Payload):
    username: str = Field(min_length=1, max_length=80)
    password: str = Field(min_length=1, max_length=256)


class Enter(Payload):
    passphrase: str = Field(min_length=1, max_length=256)
    model_config = ConfigDict(extra='forbid')


class MemberName(Payload):
    name: str = Field(min_length=1, max_length=80)


class NewUser(Payload):
    username: str = Field(pattern=r'^[A-Za-z0-9_.-]{3,80}$')
    name: str = Field(min_length=1, max_length=80)
    password: str = Field(min_length=10, max_length=256)
    role: Literal['admin', 'member'] = 'member'


class UserUpdate(Payload):
    active: bool
    role: Literal['admin', 'member']


class PasswordChange(Payload):
    current: str = Field(max_length=256)
    password: str = Field(min_length=10, max_length=256)


class ReviewPayload(Payload):
    revision_id: str
    verdict: Literal['approve', 'changes', 'reject', 'evidence', 'reply']
    body: str = Field(min_length=1, max_length=15000)
    quote: str = Field(default='', max_length=10000)
    suggestion: str = Field(default='', max_length=50000)
    evidence: str = Field(default='', max_length=10000)
    parent_id: str | None = None


class RevisionPayload(Payload):
    base_revision: str
    content: str = Field(max_length=2_000_000)
    source: str = Field(default='', max_length=10000)
    operation: Literal['upsert', 'delete'] = 'upsert'
    model_config = ConfigDict(extra='forbid', str_strip_whitespace=False)  # Preserve source Markdown byte semantics.


class DecisionPayload(Payload):
    revision_id: str
    status: Literal['pending', 'approved', 'rejected', 'needs_changes']
    reason: str = Field(min_length=1, max_length=10000)


class Selection(Payload):
    item_ids: list[str] = Field(min_length=1, max_length=2000)


class ReleasePayload(Payload):
    name: str = Field(min_length=1, max_length=120)
    notes: str = Field(default='', max_length=10000)
    revision_ids: list[str] = Field(default_factory=list, max_length=2000)
    rollback_id: str | None = None


class ResolutionPayload(Payload):
    resolution: Literal['open', 'accepted', 'partial', 'declined', 'more_evidence']
    note: str = Field(min_length=1, max_length=10000)


class Assignment(Payload):
    user_id: str | None = None


class SettingsPayload(Payload):
    name: str = Field(min_length=1, max_length=100)
    profile: Literal['legacy-preserve', 'finesub-code-v17', 'finesub-0.5.1', 'finesub-0.6.0']


@app.get('/api/health')
def health(db=Depends(db_session, scope='function')):
    db.execute(text('SELECT 1'))
    return {'status': 'ok', 'version': app.version}


@app.get('/api/bootstrap')
def bootstrap(db=Depends(db_session, scope='function')):
    return {'initialized': bool(db.scalar(select(User.id).where(User.role == 'admin', User.active.is_(True)).limit(1))),
            'access_configured': db.get(AccessGate, 1) is not None}


def set_session_cookie(response, name, token, seconds):
    response.set_cookie(name, token, httponly=True, secure=os.getenv('COOKIE_SECURE', 'false').lower() == 'true',
                        samesite='strict', max_age=seconds, path='/')


@app.post('/api/auth/enter')
def enter(body: Enter, request: Request, response: Response, db=Depends(db_session, scope='function')):
    gate = db.get(AccessGate, 1)
    if not gate:
        raise HTTPException(503, '访问口令尚未设置，请联系管理员')
    identity = token_hash('access:' + (request.client.host if request.client else ''))
    if not verify_password(body.passphrase, gate.passphrase_hash):
        # Throttle failures only: a correct passphrase must never be locked out
        # by somebody else's guesses from the same address.
        recent = db.scalars(select(Audit).where(Audit.action == 'access.failure', Audit.target_id == identity)
                            .order_by(Audit.created_at.desc()).limit(10)).all()
        if len([a for a in recent if a.detail.get('time', 0) > time.time() - 900]) >= 10:
            raise HTTPException(429, '口令尝试过多，请 15 分钟后重试')
        db.add(Audit(action='access.failure', target_id=identity, detail={'time': time.time()}))
        db.commit()
        raise HTTPException(401, '访问口令不正确')
    try:
        previous, member = access_session(request, db)
        db.delete(previous)
    except HTTPException:
        suffix = secrets.token_hex(8)
        member = User(username='visitor-' + suffix, name='成员 ' + suffix[:6], password='', role='member')
        db.add(member)
        db.flush()
    token, csrf = secrets.token_urlsafe(48), secrets.token_urlsafe(32)
    seconds = 30 * 86400
    db.add(AccessSession(id=token_hash(token), user_id=member.id, csrf=csrf, expires=int(time.time()) + seconds))
    set_session_cookie(response, 'review_access', token, seconds)
    old_token = request.cookies.get('review_session')
    if old_token:
        db.execute(delete(LoginSession).where(LoginSession.id == token_hash(old_token)))
    response.delete_cookie('review_session', path='/')
    audit(db, member, 'access.enter', member.id)
    return {'user': user_view(member), 'csrf': csrf}


@app.post('/api/auth/login')
def login(body: Login, request: Request, response: Response, member=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    # DB-backed throttle survives restarts and applies to all API workers.
    # Only failed attempts count, and the key is the address: an attacker cannot
    # lock an admin out by guessing that admin's username.
    identity = token_hash('auth:' + (request.client.host if request.client else ''))
    user = db.scalar(select(User).where(User.username == body.username.lower()))
    if not user or not user.password or not verify_password(body.password, user.password) or not user.active or user.role != 'admin':
        cutoff = time.time() - 900
        recent = db.scalars(select(Audit).where(Audit.action == 'auth.failure', Audit.target_id == identity).order_by(Audit.created_at.desc()).limit(10)).all()
        if len([a for a in recent if a.detail.get('time', 0) > cutoff]) >= 10:
            raise HTTPException(429, '登录尝试过多，请 15 分钟后重试')
        db.add(Audit(action='auth.failure', target_id=identity, detail={'time': time.time()}))
        db.commit()
        raise HTTPException(401, '管理员账号或密码错误，或账号已停用')
    token, csrf = secrets.token_urlsafe(48), secrets.token_urlsafe(32)
    db.execute(delete(LoginSession).where(LoginSession.expires <= int(time.time())))
    old_token = request.cookies.get('review_session')
    if old_token:
        db.execute(delete(LoginSession).where(LoginSession.id == token_hash(old_token)))
    db.add(LoginSession(id=token_hash(token), user_id=user.id, csrf=csrf, expires=int(time.time()) + 43200))
    set_session_cookie(response, 'review_session', token, 43200)
    audit(db, user, 'auth.login', user.id)
    return {'user': user_view(user), 'csrf': csrf}


@app.get('/api/auth/me')
def me(request: Request, user=Depends(current_user, scope='function')):
    return {'user': user_view(user), 'csrf': request.state.login.csrf}


@app.post('/api/auth/logout')
def logout(request: Request, response: Response, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    token = request.cookies.get('review_session')
    if token:
        db.execute(delete(LoginSession).where(LoginSession.id == token_hash(token)))
    response.delete_cookie('review_session', path='/')
    session, member = access_session(request, db)
    return {'user': user_view(member), 'csrf': session.csrf}


@app.post('/api/auth/leave')
def leave(request: Request, response: Response, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    session, _ = access_session(request, db)
    db.delete(session)
    token = request.cookies.get('review_session')
    if token:
        db.execute(delete(LoginSession).where(LoginSession.id == token_hash(token)))
    response.delete_cookie('review_access', path='/')
    response.delete_cookie('review_session', path='/')
    return {'ok': True}


@app.post('/api/auth/name')
def member_name(body: MemberName, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    if user.password:
        raise HTTPException(403, '此入口仅用于普通成员署名')
    user.name = body.name
    audit(db, user, 'access.name', user.id)
    return user_view(user)


@app.post('/api/auth/password')
def password(body: PasswordChange, response: Response, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    if not verify_password(body.current, user.password):
        raise HTTPException(400, '当前密码不正确')
    user.password = hash_password(body.password)
    db.execute(delete(LoginSession).where(LoginSession.user_id == user.id))
    response.delete_cookie('review_session', path='/')
    audit(db, user, 'auth.password', user.id)
    return {'ok': True}


@app.get('/api/users')
def users(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    rows = db.scalars(select(User).order_by(User.created_at)).all()
    return [user_view(row) if user.role == 'admin' else {'id': row.id, 'name': row.name, 'active': row.active} for row in rows]


@app.post('/api/users')
def create_user(body: NewUser, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    workspace_lock(db)
    if db.scalar(select(User.id).where(User.username == body.username.lower())):
        raise HTTPException(409, '账号已存在')
    row = User(username=body.username.lower(), name=body.name, password=hash_password(body.password), role=body.role)
    db.add(row)
    db.flush()
    audit(db, user, 'user.create', row.id, role=row.role)
    return user_view(row)


@app.patch('/api/users/{user_id}')
def change_user(user_id: str, body: UserUpdate, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    workspace_lock(db)
    row = require(db, User, user_id)
    if not row.password and body.role != 'member':
        raise HTTPException(409, '口令成员不能直接设为管理员，请创建独立管理员账号')
    if row.id == user.id and (not body.active or body.role != 'admin'):
        raise HTTPException(409, '不能在当前会话停用自己或取消自己的管理员权限')
    row.active, row.role = body.active, body.role
    db.execute(delete(LoginSession).where(LoginSession.user_id == row.id))
    audit(db, user, 'user.update', row.id, active=row.active, role=row.role)
    return user_view(row)


@app.get('/api/settings')
def settings(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    ws = db.get(Workspace, 1)
    return {'name': ws.name, 'profile': ws.profile, 'profiles': compat.PROFILES, 'latest_release': ws.latest_release}


@app.put('/api/settings')
def update_settings(body: SettingsPayload, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    ws = workspace_lock(db)
    ws.name, ws.profile = body.name, body.profile
    audit(db, user, 'settings.update', '1', **body.model_dump())
    return {'ok': True}


@app.get('/api/dashboard')
def dashboard(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    revisions = current_revisions(db)
    counts = Counter(r.status for r in revisions)
    published = {e.id: e.published_revision for e in db.scalars(select(Entry)).all()}
    mine = db.scalar(select(func.count()).select_from(Entry).where(Entry.assignee_id == user.id))
    review_count = db.scalar(select(func.count()).select_from(Review).where(Review.verdict != 'reply'))
    recent = db.scalars(select(Batch).order_by(Batch.created_at.desc()).limit(5)).all()
    pending_publish = sum(r.status == 'approved' and published.get(r.entry_id) != r.id for r in revisions)
    return {'total': len(revisions), 'counts': dict(counts), 'mine': mine, 'reviews': review_count, 'pending_publish': pending_publish,
            'recent_batches': [{'id': b.id, 'name': b.name, 'status': b.status, 'source': b.source, 'created_at': b.created_at} for b in recent]}


@app.get('/api/entries')
def entries(q: str = '', status: str = '', category: str = '', batch_id: str = '', mine: bool = False,
            reviewed: Literal['', 'yes', 'no'] = '', after: str = '', group_id: str = '',
            page: int = Query(1, ge=1), limit: int = Query(30, ge=1, le=100), user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    if after and page != 1:
        # Both define a starting point; applying the offset after the cursor
        # would silently skip items, so reject the mixture.
        raise HTTPException(422, '游标翻页（after）不能与 page 同时使用')
    query = select(Revision, Entry).join(Entry, Entry.current_revision == Revision.id)
    if q:
        term = '%' + q.replace('\\', '\\\\').replace('%', '\\%').replace('_', '\\_') + '%'
        query = query.where(or_(Revision.title.ilike(term, escape='\\'), Revision.aliases.ilike(term, escape='\\'), Revision.content.ilike(term, escape='\\'), Revision.source.ilike(term, escape='\\')))
    if status:
        query = query.where(Revision.status == status)
    if category:
        query = query.where(Revision.category == category)
    if batch_id:
        query = query.where(Revision.batch_id == batch_id)
    if mine:
        query = query.where(Entry.assignee_id == user.id)
    if group_id:
        query = query.where(Revision.group_id == group_id)
    own_review = select(Review.id).where(Review.revision_id == Revision.id,
                                       Review.author_id == user.id, Review.verdict != 'reply').exists()
    if reviewed:
        query = query.where(own_review if reviewed == 'yes' else ~own_review)
    if after:
        # The cursor remains valid after a decision, review or new revision removes
        # the current item from the queue. Offset pagination would skip its successor.
        cursor = require(db, Revision, after)
        query = query.where(or_(Revision.created_at < cursor.created_at,
                                and_(Revision.created_at == cursor.created_at, Revision.id > cursor.id)))
    total = db.scalar(select(func.count()).select_from(query.subquery()))
    rows = db.execute(query.order_by(Revision.created_at.desc(), Revision.id).offset((page - 1) * limit).limit(limit)).all()
    ids = [r.id for r, e in rows]
    counts = dict(db.execute(select(Review.revision_id, func.count()).where(Review.revision_id.in_(ids), Review.verdict != 'reply').group_by(Review.revision_id)).all()) if ids else {}
    own_ids = set(db.scalars(select(Review.revision_id).where(Review.revision_id.in_(ids), Review.author_id == user.id, Review.verdict != 'reply')).all()) if ids else set()
    return {'total': total, 'page': page, 'items': [{**revision_view(r), 'assignee_id': e.assignee_id, 'review_count': counts.get(r.id, 0), 'my_review': r.id in own_ids, 'published': e.published_revision == r.id} for r, e in rows]}


@app.get('/api/entries/{entry_id}')
def entry_detail(entry_id: str, revision_id: str = '', compare_to: Literal['previous', 'published'] = 'previous', user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    entry = require(db, Entry, entry_id)
    revision = require(db, Revision, revision_id or entry.current_revision)
    if revision.entry_id != entry.id:
        raise HTTPException(404, '版本不属于此词条')
    versions = db.scalars(select(Revision).where(Revision.entry_id == entry.id).order_by(Revision.number.desc())).all()
    records = db.scalars(select(Review).join(Revision, Review.revision_id == Revision.id).where(Revision.entry_id == entry.id).order_by(Review.created_at)).all()
    decisions = db.scalars(select(Decision).join(Revision, Decision.revision_id == Revision.id).where(Revision.entry_id == entry.id).order_by(Decision.created_at.desc())).all()
    people = {u.id: u.name for u in db.scalars(select(User)).all()}
    base_id = entry.published_revision if compare_to == 'published' else revision.parent_id
    old = db.get(Revision, base_id) if base_id else None
    related = db.scalars(select(Revision).join(Entry, Entry.current_revision == Revision.id).where(
        Revision.group_id == revision.group_id, Revision.entry_id != entry.id)).all() if revision.group_id else []
    diff = list(difflib.unified_diff((old.content if old else '').splitlines(), revision.content.splitlines(), fromfile=f'v{old.number}' if old else '新词条', tofile=f'v{revision.number}', lineterm=''))
    warnings, errors = compat.validate(revision.path, revision.content, db.get(Workspace, 1).profile,
                                        baseline=compatibility_baseline(db, revision)) if revision.operation != 'delete' else ([], [])
    aliases = set(re.split(r'[,、，]', revision.aliases)) - {''}
    if aliases and revision.category != 'style':
        # Narrow candidates in SQL, then confirm exact alias/title overlap.
        overlap = db.scalars(select(Revision).join(Entry, Entry.current_revision == Revision.id).where(
            Entry.id != entry.id, Revision.category != 'style',
            or_(Revision.title.in_(aliases), *[Revision.aliases.contains(alias, autoescape=True) for alias in aliases]))).all()
        for other in overlap:
            if aliases.intersection(set(re.split(r'[,、，]', other.aliases)) | {other.title}):
                warnings.append(f'别名与「{other.title}」有重叠，请核实是否同一对象')
    return {'entry': {**revision_view(revision), 'content': revision.content, 'current_revision': entry.current_revision, 'assignee_id': entry.assignee_id,
                      'published': entry.published_revision == revision.id},
            'versions': [revision_view(r) for r in versions], 'diff': diff, 'warnings': warnings, 'errors': errors,
            'comparison': {'kind': compare_to, 'number': old.number if old else None, 'id': base_id},
            'related': [revision_view(r) for r in related],
            'reviews': [{**{k: getattr(r, k) for k in ('id', 'revision_id', 'parent_id', 'verdict', 'body', 'quote', 'suggestion', 'evidence', 'resolution', 'resolution_note', 'created_at', 'author_id')},
                         'author': people.get(r.author_id, '已停用成员'), 'stale': r.revision_id != entry.current_revision} for r in records],
            'decisions': [{**{k: getattr(d, k) for k in ('revision_id', 'status', 'reason', 'created_at')}, 'author': people.get(d.author_id)} for d in decisions]}


@app.post('/api/entries/{entry_id}/reviews')
def add_review(entry_id: str, body: ReviewPayload, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    entry = require(db, Entry, entry_id)
    revision = require(db, Revision, body.revision_id)
    if revision.entry_id != entry.id:
        raise HTTPException(422, '版本不属于当前词条')
    if body.parent_id:
        parent = require(db, Review, body.parent_id)
        if parent.revision_id != revision.id or body.verdict != 'reply':
            raise HTTPException(422, '回复必须属于同一版本的讨论')
    elif body.verdict == 'reply':
        raise HTTPException(422, '回复需要指定原记录')
    if body.quote and body.quote not in revision.content:
        raise HTTPException(422, '引用文字不在所选版本中，请重新选择')
    record = Review(author_id=user.id, **body.model_dump())
    db.add(record)
    db.flush()
    recipients = admins(db)
    if body.parent_id:
        recipients.append(parent.author_id)
    mentions = re.findall(r'@([A-Za-z0-9_.-]{3,80})', body.body)
    if mentions:
        recipients.extend(db.scalars(select(User.id).where(User.username.in_(mentions), User.active.is_(True))).all())
    notify(db, recipients, f'{user.name}为「{revision.title}」提交了审核记录', entry.id, user.id)
    audit(db, user, 'review.record', record.id, revision_id=revision.id)
    return {'id': record.id, 'status': revision.status}


@app.patch('/api/reviews/{review_id}')
def resolve_review(review_id: str, body: ResolutionPayload, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    record = require(db, Review, review_id)
    record.resolution, record.resolution_note = body.resolution, body.note
    revision = require(db, Revision, record.revision_id)
    notify(db, [record.author_id], f'管理员已处理你对「{revision.title}」的意见', revision.entry_id, user.id)
    audit(db, user, 'review.resolve', record.id, **body.model_dump())
    return {'ok': True}


@app.post('/api/entries/{entry_id}/revisions')
def revise(entry_id: str, body: RevisionPayload, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    workspace_lock(db)
    entry = require(db, Entry, entry_id)
    if entry.current_revision != body.base_revision:
        raise HTTPException(409, '内容已被其他管理员修改，请刷新后合并')
    if body.operation != 'delete':
        previous = db.get(Revision, entry.current_revision)
        _, errors = compat.validate(entry.path, body.content, db.get(Workspace, 1).profile,
                                     baseline=previous.content if previous.operation != 'delete' else None)
        if errors:
            raise HTTPException(422, '；'.join(errors))
    previous = db.get(Revision, entry.current_revision)
    revision = new_revision(db, entry, body.content, user, body.source, previous.batch_id, body.operation, group_id=previous.group_id)
    audit(db, user, 'entry.revise', revision.id, operation=body.operation)
    return revision_view(revision)


@app.post('/api/entries/{entry_id}/decision')
def decision(entry_id: str, body: DecisionPayload, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    return decide(db, entry_id, body.revision_id, body.status, body.reason, user)


@app.put('/api/entries/{entry_id}/assignee')
def assign(entry_id: str, body: Assignment, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    workspace_lock(db)
    entry = require(db, Entry, entry_id)
    if user.role != 'admin' and (body.user_id not in (None, user.id) or entry.assignee_id not in (None, user.id)):
        raise HTTPException(403, '普通成员只能认领未分配的任务或取消自己的认领')
    if body.user_id and not require(db, User, body.user_id).active:
        raise HTTPException(422, '不能分配给已停用账号')
    entry.assignee_id = body.user_id
    revision = db.get(Revision, entry.current_revision)
    notify(db, [body.user_id], f'你被分配审核「{revision.title}」', entry.id, user.id)
    audit(db, user, 'entry.assign', entry.id, user_id=body.user_id)
    return {'ok': True}


@app.post('/api/imports')
async def upload(files: list[UploadFile] = File(...), name: str = Form(..., max_length=160), source: str = Form('', max_length=10000),
                 category: str = Form('common'), user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    if not name.strip() or len(files) > 200 or category not in ('common', 'streamer', 'style', 'translation'):
        raise HTTPException(422, '请填写批次名，最多上传 200 个文件，并选择有效分类')
    batch = Batch(name=name.strip(), source=source, author_id=user.id)
    db.add(batch)
    db.flush()
    total = 0
    for file in files:
        filename = file.filename or 'unknown'
        try:
            filename = compat.safe_path(filename)
        except ValueError as exc:
            raise HTTPException(422, str(exc)) from exc
        if not filename.lower().endswith(('.zip', '.md', '.jsonl')):
            raise HTTPException(422, '仅接受 ZIP、Markdown 和 JSONL 文件')
        raw = await file.read(compat.MAX_TOTAL + 1)
        total += len(raw)
        if total > compat.MAX_TOTAL:
            raise HTTPException(413, '单批上传总量不能超过 30 MB')
        if '/' not in filename and filename.lower().endswith('.md'):
            filename = category + '/' + filename
        db.add(Asset(batch_id=batch.id, filename=filename, data=raw, digest=compat.digest(raw)))
    db.add(Job(kind='import', target_id=batch.id))
    audit(db, user, 'import.upload', batch.id, files=len(files), bytes=total)
    return {'id': batch.id, 'status': 'queued'}


@app.get('/api/imports')
def batches(page: int = Query(1, ge=1), user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    rows = db.scalars(select(Batch).order_by(Batch.created_at.desc()).offset((page - 1) * 30).limit(30)).all()
    return [{'id': b.id, 'name': b.name, 'source': b.source, 'status': b.status, 'created_at': b.created_at} for b in rows]


@app.get('/api/imports/{batch_id}')
def batch_detail(batch_id: str, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    batch = require(db, Batch, batch_id)
    items = db.scalars(select(ImportItem).where(ImportItem.batch_id == batch.id).order_by(ImportItem.path)).all()
    job = db.scalar(select(Job).where(Job.target_id == batch.id).order_by(Job.created_at.desc()))
    return {'id': batch.id, 'name': batch.name, 'source': batch.source, 'status': batch.status, 'warnings': batch.warnings,
            'job': {'id': job.id, 'status': job.status, 'error': job.error} if job else None,
            'items': [{k: getattr(i, k) for k in ('id', 'path', 'classification', 'warnings', 'error', 'grouped', 'operation', 'base_revision')} for i in items],
            'assets': [{'id': a.id, 'filename': a.filename, 'digest': a.digest} for a in db.execute(select(Asset.id, Asset.filename, Asset.digest).where(Asset.batch_id == batch.id)).all()]}


@app.get('/api/import-items/{item_id}')
def preview_item(item_id: str, user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    item = require(db, ImportItem, item_id)
    old = db.get(Revision, item.base_revision) if item.base_revision else None
    return {'path': item.path, 'content': item.content, 'diff': list(difflib.unified_diff((old.content if old else '').splitlines(), item.content.splitlines(), fromfile='基础版本', tofile='导入版本', lineterm=''))}


@app.post('/api/imports/{batch_id}/confirm')
def confirm(batch_id: str, body: Selection, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    return confirm_batch(db, batch_id, body.item_ids, user)


@app.get('/api/assets/{asset_id}/download')
def original(asset_id: str, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    asset = require(db, Asset, asset_id)
    return Response(asset.data, media_type='application/octet-stream', headers={'Content-Disposition': f"attachment; filename*=UTF-8''{quote(asset.filename.split('/')[-1])}"})


@app.post('/api/jobs/{job_id}/retry')
def retry(job_id: str, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    workspace_lock(db)
    job = require(db, Job, job_id)
    if job.status != 'failed':
        raise HTTPException(409, '只能重试失败任务')
    if job.kind == 'release' and db.scalar(select(Release.id).where(Release.status.in_(['queued', 'processing']))):
        raise HTTPException(409, '已有发布任务正在执行')
    job.status, job.error = 'queued', ''
    target = require(db, Batch if job.kind == 'import' else Release, job.target_id)
    target.status = 'queued'
    audit(db, user, 'job.retry', job.id)
    return {'ok': True}


@app.get('/api/releases')
def releases(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    rows = db.scalars(select(Release).options(load_only(Release.id, Release.name, Release.notes, Release.status, Release.created_at,
                                                      Release.snapshot, Release.report, Release.rollback_of))
                      .order_by(Release.created_at.desc()).limit(100)).all()
    ws = db.get(Workspace, 1)
    return [{'id': r.id, 'name': r.name, 'notes': r.notes, 'status': r.status, 'created_at': r.created_at,
             'count': len(r.snapshot), 'report': r.report, 'rollback_of': r.rollback_of, 'current': r.id == ws.latest_release,
             'job_id': db.scalar(select(Job.id).where(Job.target_id == r.id))} for r in rows]


@app.get('/api/releases/candidates')
def release_candidates(user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    return publication_candidates(db)


@app.post('/api/releases')
def release(body: ReleasePayload, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    result = prepare_release(db, user, body.name, body.notes, body.revision_ids, body.rollback_id)
    return {'id': result.id, 'status': result.status}


@app.get('/api/releases/{release_id}/download')
def download(release_id: str, archive: bool = False, user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    release = require(db, Release, release_id)
    if release.status != 'ready':
        raise HTTPException(409, '发布包尚未生成')
    audit(db, user, 'release.download', release.id, archive=archive)
    return Response(release.archive if archive else release.artifact, media_type='application/zip', headers={'Content-Disposition': f'attachment; filename="finesub-{release.id[:8]}{"-audit" if archive else ""}.zip"'})


@app.get('/api/notices')
def notices(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    rows = db.scalars(select(Notice).where(Notice.user_id == user.id).order_by(Notice.created_at.desc()).limit(60)).all()
    return [{k: getattr(r, k) for k in ('id', 'entry_id', 'text', 'read', 'created_at')} for r in rows]


@app.post('/api/notices/read')
def read_notices(user=Depends(current_user, scope='function'), db=Depends(db_session, scope='function')):
    db.execute(update(Notice).where(Notice.user_id == user.id).values(read=True))
    return {'ok': True}


@app.get('/api/audit')
def audit_log(page: int = Query(1, ge=1), user=Depends(admin, scope='function'), db=Depends(db_session, scope='function')):
    rows = db.scalars(select(Audit).where(Audit.action != 'auth.failure').order_by(Audit.created_at.desc()).offset((page - 1) * 50).limit(50)).all()
    people = {u.id: u.name for u in db.scalars(select(User)).all()}
    return [{**{k: getattr(a, k) for k in ('id', 'action', 'target_id', 'detail', 'created_at')}, 'actor': people.get(a.actor_id, '系统')} for a in rows]


DIST = Path(__file__).resolve().parents[2] / 'frontend' / 'dist'
# Windows registry associations can mark .js as text/plain. Module scripts
# require a JavaScript MIME type, regardless of the host's file associations.
mimetypes.init()
mimetypes.add_type('text/javascript', '.js')
mimetypes.add_type('text/javascript', '.mjs')
mimetypes.add_type('text/css', '.css')
if DIST.exists():
    app.mount('/assets', StaticFiles(directory=DIST / 'assets'), name='assets')

    @app.get('/{path:path}')
    def spa(path: str):
        if path == 'api' or path.startswith('api/'):
            raise HTTPException(404, '接口不存在')
        return FileResponse(DIST / 'index.html')

