import time

from fastapi.testclient import TestClient
from sqlalchemy import delete, select

from test_workflow import (PASSPHRASE, PASSWORD, client, isolated_database,
                           latest, upload)
from app.db import AccessGate, AccessSession, LoginSession, Session, User
from app.main import app
from app.security import hash_password, token_hash


def test_gate_is_required_even_with_an_existing_administrator_cookie():
    anonymous = TestClient(app)
    assert anonymous.get('/api/entries').status_code == 401
    assert anonymous.post('/api/auth/login', json={'username': 'admin', 'password': PASSWORD}).status_code == 401
    a = client()
    anonymous.cookies.set('review_session', a.cookies.get('review_session'))
    assert anonymous.get('/api/audit').status_code == 401


def test_wrong_passphrase_is_rate_limited_without_creating_members():
    c = TestClient(app)
    for _ in range(10):
        assert c.post('/api/auth/enter', json={'passphrase': 'incorrect'}).status_code == 401
    assert c.post('/api/auth/enter', json={'passphrase': 'incorrect'}).status_code == 429
    assert not c.cookies.get('review_access')
    with Session() as db:
        assert not db.scalar(select(User.id).where(User.password == ''))
    # Failures are throttled, but another address/user cannot lock the correct
    # passphrase out: verification runs before the failure counter is consulted.
    assert c.post('/api/auth/enter', json={'passphrase': PASSPHRASE}).status_code == 200


def test_login_throttle_counts_failures_without_locking_out_the_right_password():
    m = client('member')
    for _ in range(10):
        assert m.post('/api/auth/login', json={'username': 'admin', 'password': 'incorrect'}).status_code == 401
    assert m.post('/api/auth/login', json={'username': 'admin', 'password': 'incorrect'}).status_code == 429
    result = m.post('/api/auth/login', json={'username': 'admin', 'password': PASSWORD})
    assert result.status_code == 200 and result.json()['user']['role'] == 'admin'


def test_unconfigured_gate_fails_closed():
    with Session.begin() as db:
        db.execute(delete(AccessGate))
    c = TestClient(app)
    assert c.get('/api/bootstrap').json()['access_configured'] is False
    assert c.post('/api/auth/enter', json={'passphrase': PASSPHRASE}).status_code == 503


def test_member_identities_are_distinct_and_records_survive_admin_login_logout():
    a, m, another = client(), client('member'), client('member')
    member_id = m.get('/api/auth/me').json()['user']['id']
    assert another.get('/api/auth/me').json()['user']['id'] != member_id
    assert m.post('/api/auth/name', json={'name': '校对小林'}).json()['name'] == '校对小林'
    upload(a)
    entry = latest(a)
    assert m.post(f'/api/entries/{entry["entry_id"]}/reviews', json={
        'revision_id': entry['id'], 'verdict': 'approve', 'body': '核对意见'}).status_code == 200
    result = m.post('/api/auth/login', json={'username': 'admin', 'password': PASSWORD})
    assert result.json()['user']['role'] == 'admin'
    m.headers['X-CSRF-Token'] = result.json()['csrf']
    old_admin_cookie = m.cookies.get('review_session')
    assert m.get('/api/audit').status_code == 200
    result = m.post('/api/auth/logout')
    assert result.json()['user']['id'] == member_id
    assert result.json()['user']['name'] == '校对小林'
    m.headers['X-CSRF-Token'] = result.json()['csrf']
    assert m.get('/api/audit').status_code == 403
    assert m.get(f'/api/entries/{entry["entry_id"]}').json()['reviews'][0]['author_id'] == member_id
    with Session() as db:
        assert db.get(LoginSession, token_hash(old_admin_cookie)) is None
    assert m.post('/api/auth/leave').status_code == 200
    assert m.get('/api/entries').status_code == 401


def test_login_requires_csrf_and_rejects_member_accounts():
    m = client('member')
    csrf = m.headers.pop('X-CSRF-Token')
    assert m.post('/api/auth/login', json={'username': 'admin', 'password': PASSWORD}).status_code == 403
    m.headers['X-CSRF-Token'] = csrf
    assert m.post('/api/auth/login', json={'username': 'member', 'password': PASSWORD}).status_code == 401
    assert m.get('/api/auth/me').json()['user']['role'] == 'member'
    assert m.post('/api/auth/password', json={'current': '', 'password': PASSWORD}).status_code == 403


def test_passcode_members_cannot_be_promoted_or_request_their_role():
    a, m = client(), client('member')
    member = m.get('/api/auth/me').json()['user']
    assert a.patch('/api/users/' + member['id'], json={'active': True, 'role': 'admin'}).status_code == 409
    assert m.post('/api/auth/enter', json={'passphrase': PASSPHRASE, 'role': 'admin'}).status_code == 422


def test_admin_expiry_and_password_change_restore_member_access():
    a = client()
    with Session.begin() as db:
        row = db.get(LoginSession, token_hash(a.cookies.get('review_session')))
        row.expires = int(time.time()) - 1
    result = a.get('/api/auth/me').json()
    assert result['user']['role'] == 'member'
    a.headers['X-CSRF-Token'] = result['csrf']
    result = a.post('/api/auth/login', json={'username': 'admin', 'password': PASSWORD})
    a.headers['X-CSRF-Token'] = result.json()['csrf']
    assert a.post('/api/auth/password', json={'current': PASSWORD, 'password': PASSWORD + '-new'}).status_code == 200
    assert a.get('/api/auth/me').json()['user']['role'] == 'member'


def test_access_expiry_and_rotation_invalidate_admin_access():
    a = client()
    with Session.begin() as db:
        row = db.get(AccessSession, token_hash(a.cookies.get('review_access')))
        row.expires = int(time.time()) - 1
    assert a.get('/api/audit').status_code == 401
    a = client()
    with Session.begin() as db:
        db.get(AccessGate, 1).passphrase_hash = hash_password(PASSPHRASE + '-new')
        db.execute(delete(AccessSession))
    assert a.get('/api/audit').status_code == 401
    assert a.post('/api/auth/enter', json={'passphrase': PASSPHRASE}).status_code == 401
    assert a.post('/api/auth/enter', json={'passphrase': PASSPHRASE + '-new'}).json()['user']['role'] == 'member'


def test_entry_cookie_flags_and_reentry_do_not_duplicate_member_identity():
    c = TestClient(app)
    first = c.post('/api/auth/enter', json={'passphrase': PASSPHRASE})
    cookie = first.headers['set-cookie'].lower()
    assert 'httponly' in cookie and 'samesite=strict' in cookie and 'max-age=2592000' in cookie
    second = c.post('/api/auth/enter', json={'passphrase': PASSPHRASE})
    assert second.json()['user']['id'] == first.json()['user']['id']
    assert second.json()['csrf'] != first.json()['csrf']
