"""Version-specific rules and native schema 6 acceptance, using isolated stores."""
import json
import os
import sqlite3
import subprocess
import sys
from pathlib import Path

import pytest

from app import compat, node_compat, node_markdown
from test_node_knowledge import COMMON, STREAMER, STYLE
from test_preserved_values import OLD
from test_workflow import approve, client, isolated_database, publish, upload, zip_files

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from preserved_values import read_archive

PROFILE = node_markdown.CURRENT_PROFILE
NEW_COMMON = COMMON.replace('## 角色\n', '## 角色\n- [背景] 乐队成员来自同一所学校。\n')


def test_schema6_presets_allow_common_notes_without_weakening_schema5():
    assert not compat.validate('common/作品.md', NEW_COMMON, PROFILE)[1]
    assert compat.validate('common/作品.md', NEW_COMMON, node_markdown.PROFILE)[1]
    assert compat.validate('common/作品.md', '# 作品\n## 角色\nHero|英雄|主角别称\n', PROFILE)[1]


def test_human_marks_are_history_not_imported_knowledge():
    marked = NEW_COMMON.replace('## 角色\n', '## 角色\n<!-- 人认可版 @12345678\n[旧标记] 旧的认可内容\n-->\n')
    assert not compat.validate('common/作品.md', marked, PROFILE)[1]
    assert node_compat.projection(marked) == node_compat.projection(NEW_COMMON)
    assert compat.validate('common/作品.md', marked.replace('-->', ''), PROFILE)[1]
    moved = NEW_COMMON + '重分类理由: 作品背景应归入 common\n'
    assert not compat.validate('common/作品.md', moved, PROFILE)[1]
    assert compat.validate('common/作品.md', moved, node_markdown.PROFILE)[1]
    assert compat.validate('common/作品.md', NEW_COMMON + '未知字段: 值\n', PROFILE)[1]


@pytest.mark.parametrize('profile', list(node_markdown.NODE_PROFILES))
def test_new_manifest_is_versioned_and_old_manifest_remains_schema5(tmp_path, profile):
    docs = {'streamer/主播.md': OLD}
    files, report = compat.build_files({'streamer/主播.md': OLD.replace('156cm', '157cm')}, profile, baselines=docs)
    assert not report['errors']
    metadata = json.loads(files['finesub-review-profile.json'])
    assert metadata == node_markdown.release_profile(profile)
    archive = tmp_path / 'compat.zip'
    archive.write_bytes(compat.make_zip(files))
    assert len(read_archive(archive, expected_profile=profile)) == 1
    other = PROFILE if profile == node_markdown.PROFILE else node_markdown.PROFILE
    with pytest.raises(ValueError, match='不匹配'):
        read_archive(archive, expected_profile=other)
    manifest = json.loads(files['finesub-review-compat.json'])
    manifest['knowledge_schema'] = 7
    files['finesub-review-compat.json'] = json.dumps(manifest)
    archive.write_bytes(compat.make_zip(files))
    with pytest.raises(ValueError, match='版本不一致'):
        read_archive(archive)
    if profile == node_markdown.PROFILE:
        manifest = {k: v for k, v in manifest.items() if k not in ('knowledge_schema', 'profile')}
        manifest['schema'] = 1
        files['finesub-review-compat.json'] = json.dumps(manifest)
        files.pop('finesub-review-profile.json')
        archive.write_bytes(compat.make_zip(files))
        assert len(read_archive(archive, expected_profile=profile)) == 1
        with pytest.raises(ValueError, match='不匹配'):
            read_archive(archive, expected_profile=PROFILE)


@pytest.fixture
def native6():
    names = ('FINESUB6_REFERENCE_SRC', 'FINESUB6_COMPAT_SRC')
    if not all(os.getenv(name) for name in names):
        pytest.skip('Supply explicit schema 6 reference and patched source')
    return tuple(os.environ[name] for name in names)


def test_schema6_review_release_and_native_write_read(tmp_path, native6):
    admin = client()
    settings = admin.put('/api/settings', json={'name': 'schema 6 验收', 'profile': PROFILE})
    assert settings.status_code == 200
    assert PROFILE in admin.get('/api/settings').json()['profiles']
    upload(admin, {'common/作品.md': NEW_COMMON, 'streamer/主播.md': STREAMER, 'style/团队风格.md': STYLE})
    entries = admin.get('/api/entries').json()['items']
    assert len(entries) == 3
    for entry in entries:
        approve(admin, entry)
    release = publish(admin, entries)
    files = zip_files(admin, release)
    assert json.loads(files['finesub-review-profile.json'])['knowledge_schema'] == 6
    assert 'style/index.md' not in files
    archive = tmp_path / 'schema6.zip'
    archive.write_bytes(admin.get('/api/releases/' + release + '/download').content)
    command = [sys.executable, str(ROOT / 'scripts/verify_finesub.py'), '--finesub-src', native6[0], '--archive', str(archive)]
    result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=60)
    assert result.returncode == 0, result.stdout + result.stderr
    report = json.loads(result.stdout)
    assert report['schema'] == 6 and report['subjects'] == 3 and report['profile'] == PROFILE
    assert report['finesub_version'] == '0.6.0'
    (ROOT / 'test-results/native-schema6.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    # A package declaration wins over inference, and cannot be forced into another profile.
    wrong = subprocess.run([*command, '--profile', node_markdown.PROFILE], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert wrong.returncode and '版本不一致' in wrong.stderr
    old_source = os.getenv('FINESUB_REFERENCE_SRC')
    if old_source:
        wrong = subprocess.run([sys.executable, str(ROOT / 'scripts/verify_finesub.py'), '--finesub-src', old_source, '--archive', str(archive)], capture_output=True, text=True, encoding='utf-8', timeout=30)
        assert wrong.returncode and '要求 schema 6' in wrong.stderr


@pytest.mark.parametrize('stored_schema', ['5', '7'])
def test_schema6_import_refuses_mismatched_store_before_native_open(tmp_path, native6, stored_schema):
    root = tmp_path / 'knowledge'
    root.mkdir()
    database = root / 'knowledge.sqlite'
    with sqlite3.connect(database) as db:
        db.execute('CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)')
        db.execute('INSERT INTO meta VALUES (?, ?)', ('schema_version', stored_schema))
    db.close()
    before = database.read_bytes()
    docs = {'streamer/主播.md': OLD}
    files, _ = compat.build_files(docs, PROFILE, baselines=docs)
    archive = tmp_path / 'compat.zip'
    archive.write_bytes(compat.make_zip(files))
    command = [sys.executable, str(ROOT / 'scripts/apply_compat_release.py'), '--finesub-src', native6[1], '--knowledge-root', str(root), '--archive', str(archive), '--apply']
    result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert result.returncode and '不会自动迁移' in result.stderr
    assert database.read_bytes() == before


def test_schema6_human_endorsements_and_tentative_aliases_survive(tmp_path, native6):
    report_path = tmp_path / 'human-marks.json'
    result = subprocess.run([sys.executable, str(ROOT / 'backend/tests/native_schema6_probe.py'), '--engine-src', native6[1], '--root', str(tmp_path / 'knowledge'), '--report', str(report_path)], capture_output=True, text=True, encoding='utf-8', timeout=60)
    assert result.returncode == 0, result.stdout + result.stderr
    report = json.loads(report_path.read_text(encoding='utf-8'))
    assert report and all(report.values())
    (ROOT / 'test-results/native-schema6-human-marks.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
