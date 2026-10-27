"""Read-only live smoke check; does not publish or approve production data."""
import json
import os
import re
import sqlite3
import sys
from pathlib import Path

import httpx

root = Path(__file__).resolve().parents[1]
credentials = (root / 'data' / 'initial-account.txt').read_text(encoding='utf-8')
username = re.search(r'^账号: (.+)$', credentials, re.M).group(1)
password = re.search(r'^密码: (.+)$', credentials, re.M).group(1)
# Windows 动态端口保留可能占用 8765（见 README），允许指定实际端口。
base_url = sys.argv[1] if len(sys.argv) > 1 else os.getenv('REVIEW_BASE_URL', 'http://127.0.0.1:8765')
# trust_env=False：环境里的 HTTP_PROXY 会把本机请求转发出去并得到假 404。
with httpx.Client(base_url=base_url, trust_env=False) as client:
    assert client.get('/api/health').json()['status'] == 'ok'
    assert client.get('/api/entries').status_code == 401
    passphrase = os.getenv('REVIEW_PASSPHRASE') or (root / 'data' / 'access-passphrase.txt').read_text(encoding='utf-8').rstrip('\r\n')
    response = client.post('/api/auth/enter', json={'passphrase': passphrase})
    response.raise_for_status()
    member_id = response.json()['user']['id']
    assert response.json()['user']['role'] == 'member'
    client.headers['X-CSRF-Token'] = response.json()['csrf']
    assert client.get('/api/audit').status_code == 403
    response = client.post('/api/auth/login', json={'username': username, 'password': password})
    response.raise_for_status()
    client.headers['X-CSRF-Token'] = response.json()['csrf']
    stats = client.get('/api/dashboard').json()
    entries = client.get('/api/entries?limit=100').json()['items']
    for path in ('/api/settings', '/api/users', '/api/notices', '/api/imports', '/api/releases', '/api/audit'):
        assert client.get(path).status_code == 200, path
    assert all(e['status'] == 'pending' for e in entries)
    first = client.get('/api/entries/' + entries[0]['entry_id']).json()
    assert first['entry']['content']
    homepage = client.get('/')
    assert homepage.status_code == 200, homepage.text
    assert 'text/html' in homepage.headers.get('content-type', '')
    assert 'id="app"' in homepage.text
    assets = re.findall(r'(?:src|href)="(/assets/[^"]+)"', homepage.text)
    assert any(asset.endswith('.js') for asset in assets), '首页缺少 JavaScript 入口'
    assert any(asset.endswith('.css') for asset in assets), '首页缺少样式入口'
    for asset in assets:
        resource = client.get(asset)
        assert resource.status_code == 200, asset
        expected = 'text/javascript' if asset.endswith('.js') else 'text/css'
        assert resource.headers.get('content-type', '').split(';')[0] == expected, asset
    response = client.post('/api/auth/logout', json={})
    response.raise_for_status()
    assert response.json()['user']['id'] == member_id
    client.headers['X-CSRF-Token'] = response.json()['csrf']
    assert client.get('/api/audit').status_code == 403
    assert client.get('/api/entries').status_code == 200
    client.post('/api/auth/leave', json={}).raise_for_status()
    assert client.get('/api/entries').status_code == 401
    print(json.dumps({'live_http': 'passed', 'pending_entries': stats['total'], 'reviews': stats['reviews'], 'published': stats['pending_publish']}, ensure_ascii=False))
with sqlite3.connect(root / 'test-results' / 'db' / 'test.db') as db:
    row = db.execute("SELECT artifact FROM releases WHERE status='ready' ORDER BY created_at DESC LIMIT 1").fetchone()
    if row:
        (root / 'test-results' / 'native-export.zip').write_bytes(row[0])
        print('Native-reader fixture exported from isolated test database')
