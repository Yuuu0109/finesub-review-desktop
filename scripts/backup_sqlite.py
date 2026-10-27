"""Consistent SQLite backup and restore rehearsal into a new file only."""
import argparse
import hashlib
import json
import sqlite3
from datetime import datetime
from pathlib import Path

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--source', type=Path, default=root / 'data' / 'review.db')
parser.add_argument('--output', type=Path)
args = parser.parse_args()
source = args.source.resolve(strict=True)
target = args.output or root / 'data' / 'backups' / f'review-{datetime.now():%Y%m%d-%H%M%S}.db'
target = target.resolve()
if target.exists() or target == source:
    raise SystemExit('备份目标必须是新文件，不能覆盖已有数据库')
target.parent.mkdir(parents=True, exist_ok=True)
with sqlite3.connect(source.as_uri() + '?mode=ro', uri=True) as src, sqlite3.connect(target) as dst:
    src.backup(dst)
    assert dst.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    assert not dst.execute('PRAGMA foreign_key_check').fetchall()
    tables = [r[0] for r in dst.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]
    counts = {name: dst.execute('SELECT COUNT(*) FROM "' + name + '"').fetchone()[0] for name in tables}
print(json.dumps({'backup': str(target), 'integrity': 'ok', 'sha256': hashlib.sha256(target.read_bytes()).hexdigest(), 'rows': counts}, ensure_ascii=False))
