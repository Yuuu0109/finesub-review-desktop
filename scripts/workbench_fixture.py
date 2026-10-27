"""Ephemeral browser-test server. Never imports an existing database or secrets."""
import os
import sys
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
data = Path(os.environ['REVIEW_DATA_DIR']).resolve()
assert data.is_relative_to((ROOT / 'test-results').resolve()) and data.name.startswith('workbench-')
assert not (data / 'review.db').exists(), 'Use a fresh fixture directory'
os.environ['DATABASE_URL'] = f'sqlite:///{data / "review.db"}'
sys.path.insert(0, str(ROOT / 'backend'))

from app.db import AccessGate, Session, User, init_db
from app.security import hash_password
from app.worker import run_once
import uvicorn

init_db()
with Session.begin() as db:
    db.add(AccessGate(id=1, passphrase_hash=hash_password(os.environ['REVIEW_TEST_PASSPHRASE'])))
    db.add(User(username='review-test-admin', name='审核管理员', role='admin', password=hash_password(os.environ['REVIEW_TEST_PASSWORD'])))

def worker():
    import time
    while True:
        if not run_once():
            time.sleep(.2)

threading.Thread(target=worker, daemon=True).start()
uvicorn.run('app.main:app', host='127.0.0.1', port=int(os.environ['REVIEW_TEST_PORT']), log_level='warning')
