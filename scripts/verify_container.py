"""Run inside a disposable acceptance container, never a user's deployment."""
import io
import json
import os
import secrets
import time
import zipfile

import httpx

if os.getenv('REVIEW_ACCEPTANCE') != '1':
    raise SystemExit('Only run in the explicitly named acceptance deployment with REVIEW_ACCEPTANCE=1')

from app.db import AccessGate, Session, User
from app.security import hash_password

suffix = secrets.token_hex(4)
password = secrets.token_urlsafe(20)
with Session.begin() as db:
    gate = db.get(AccessGate, 1)
    if gate:
        gate.passphrase_hash = hash_password(password)
    else:
        db.add(AccessGate(id=1, passphrase_hash=hash_password(password)))
    db.add_all([User(username=f'admin-{suffix}', name='验收管理员', role='admin', password=hash_password(password)),
                User(username=f'member-{suffix}', name='验收成员', role='member', password=hash_password(password))])


def auth(role):
    c = httpx.Client(base_url='http://127.0.0.1:8765', timeout=15)
    result = c.post('/api/auth/enter', json={'passphrase': password})
    result.raise_for_status()
    c.headers['X-CSRF-Token'] = result.json()['csrf']
    if role == 'admin':
        result = c.post('/api/auth/login', json={'username': f'{role}-{suffix}', 'password': password})
        result.raise_for_status()
        c.headers['X-CSRF-Token'] = result.json()['csrf']
    return c


admin, member = auth('admin'), auth('member')
title = f'容器验收-{suffix}'
content = f'# {title}\n验收专用词条。\n\n## 档案\n本名: {title}\n别名: 测试术语\n类型: 梗\n\n## 依据\n来源：第 20 期 12:34。\n\n## 元数据\n最近更新日期: 2026-09-17\n'
response = admin.post('/api/imports', data={'name': title}, files={'files': (title + '.md', content.encode(), 'text/markdown')})
response.raise_for_status()
batch_id = response.json()['id']
for _ in range(60):
    batch = admin.get('/api/imports/' + batch_id).json()
    if batch['status'] == 'preview':
        break
    if batch['status'] == 'failed':
        raise AssertionError(batch)
    time.sleep(.2)
assert batch['status'] == 'preview'
admin.post('/api/imports/' + batch_id + '/confirm', json={'item_ids': [i['id'] for i in batch['items']]}).raise_for_status()
entry = admin.get('/api/entries', params={'q': title}).json()['items'][0]
member.post('/api/entries/' + entry['entry_id'] + '/reviews', json={'revision_id': entry['id'], 'verdict': 'approve', 'body': '容器集成验收意见'}).raise_for_status()
assert admin.get('/api/entries/' + entry['entry_id']).json()['entry']['status'] == 'pending'
decision = {'revision_id': entry['id'], 'status': 'approved', 'reason': '验收专用裁决'}
assert member.post('/api/entries/' + entry['entry_id'] + '/decision', json=decision).status_code == 403
admin.post('/api/entries/' + entry['entry_id'] + '/decision', json=decision).raise_for_status()
response = admin.post('/api/releases', json={'name': title, 'revision_ids': [entry['id']]})
response.raise_for_status()
rid = response.json()['id']
for _ in range(60):
    release = next(r for r in admin.get('/api/releases').json() if r['id'] == rid)
    if release['status'] == 'ready':
        break
    if release['status'] == 'failed':
        raise AssertionError(release)
    time.sleep(.2)
assert release['status'] == 'ready'
result = admin.get('/api/releases/' + rid + '/download')
result.raise_for_status()
with zipfile.ZipFile(io.BytesIO(result.content)) as z:
    assert z.read('common/' + title + '.md').decode() == content
    assert title in z.read('common/index.md').decode()
assert member.get('/api/releases/' + rid + '/download').status_code == 403
print(json.dumps({'docker_http_workflow': 'passed', 'database': 'PostgreSQL', 'worker': 'import+publish', 'member_decision': '403', 'member_opinion_changes_status': False}, ensure_ascii=False))
