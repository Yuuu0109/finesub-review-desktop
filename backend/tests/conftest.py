"""Select isolated storage before any test imports the application."""
import os
from pathlib import Path

# Native FineSub subprocesses emit Chinese validation messages on Windows too.
os.environ['PYTHONUTF8'] = '1'
os.environ['PYTHONIOENCODING'] = 'utf-8'

ROOT = Path(__file__).resolve().parents[2]
os.environ['REVIEW_DATA_DIR'] = str(ROOT / 'test-results' / 'db')
test_url = os.environ.get('TEST_DATABASE_URL')
if test_url and not test_url.rsplit('/', 1)[-1].endswith('_test'):
    raise RuntimeError('Integration tests require a dedicated database ending in _test')
os.environ['DATABASE_URL'] = test_url or f'sqlite:///{ROOT / "test-results" / "db" / "test.db"}'
