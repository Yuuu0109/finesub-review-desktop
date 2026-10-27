"""Boot a fresh API process against an online backup, verify, then stop it."""
import json
import os
import re
import socket
import sqlite3
import subprocess
import sys
import time
from pathlib import Path

import httpx

root = Path(__file__).resolve().parents[1]
backup = sorted((root / 'data' / 'backups').glob('review-*.db'))[-1]
restore = root / 'test-results' / ('restore-' + str(time.time_ns()) + '.db')
with sqlite3.connect(backup.as_uri() + '?mode=ro', uri=True) as source, sqlite3.connect(restore) as target:
    source.backup(target)
    assert target.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    expected = target.execute('SELECT COUNT(*) FROM entries').fetchone()[0]
    artifacts = target.execute('SELECT COUNT(*) FROM assets').fetchone()[0]
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
env = dict(os.environ, DATABASE_URL='sqlite:///' + str(restore), REVIEW_DATA_DIR=str(restore.parent))
# Configure access only in the disposable restored database, including old backups.
subprocess.run([sys.executable, '-m', 'app.cli', 'set-passphrase', '--generate'], cwd=root / 'backend', env=env, check=True)
passphrase = (restore.parent / 'access-passphrase.txt').read_text(encoding='utf-8').rstrip('\r\n')
process = subprocess.Popen([sys.executable, '-m', 'uvicorn', 'app.main:app', '--host', '127.0.0.1', '--port', str(port)], cwd=root / 'backend', env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    with httpx.Client(base_url=f'http://127.0.0.1:{port}', timeout=3, trust_env=False) as client:
        ready = False
        for _ in range(50):
            try:
                ready = client.get('/api/health').status_code == 200
            except httpx.ConnectError:
                pass
            if ready:
                break
            time.sleep(.1)
        assert ready, 'Restored API did not start'
        credentials = (root / 'data' / 'initial-account.txt').read_text(encoding='utf-8')
        username = re.search(r'^账号: (.+)$', credentials, re.M).group(1)
        password = re.search(r'^密码: (.+)$', credentials, re.M).group(1)
        result = client.post('/api/auth/enter', json={'passphrase': passphrase})
        result.raise_for_status()
        client.headers['X-CSRF-Token'] = result.json()['csrf']
        result = client.post('/api/auth/login', json={'username': username, 'password': password})
        result.raise_for_status()
        client.headers['X-CSRF-Token'] = result.json()['csrf']
        assert client.get('/api/entries').json()['total'] == expected
        batch = client.get('/api/imports').json()[0]
        assert len(client.get('/api/imports/' + batch['id']).json()['assets']) == artifacts
        client.post('/api/auth/logout', json={}).raise_for_status()
    print(json.dumps({'restored_api_login': 'passed', 'entries': expected, 'original_assets': artifacts, 'backup_source_unchanged': True}))
finally:
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
