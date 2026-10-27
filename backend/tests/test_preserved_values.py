"""Existing-baseline admission and review/release CAS, including real samples."""
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

import pytest

from app import compat, node_markdown, node_compat
from test_workflow import approve, client, isolated_database, publish, upload, zip_files

ROOT = Path(__file__).resolve().parents[2]
OLD = '# 主播\n简介\n\n## 档案\n- 本名: 主播\n- 别名: 小主播\n- 身高: 156cm\n\n## 说话风格\n- 口癖: 原话\n\n## 元数据\n最近更新日期: 2026-09-01\n'
FILES = ['common/MyGO!!!!!.md', 'common/小日向美香1st写真集「目光」.md', 'streamer/小日向美香.md']


@pytest.mark.parametrize('target', [OLD, OLD.replace('简介', '更新简介', 1), OLD.replace('156cm', '157cm'), OLD.replace('口癖: 原话', '口癖: 更新原话')])
def test_preserving_existing_values_requires_a_baseline(target):
    _, errors = compat.validate('streamer/主播.md', target, node_markdown.PROFILE)
    assert errors
    warnings, errors = compat.validate('streamer/主播.md', target, node_markdown.PROFILE, baseline=OLD)
    assert not errors and node_compat.NOTICE in warnings


def test_readable_legacy_identity_rows_still_use_preserving_admission():
    old = OLD.replace('## 说话风格', '## 特点')
    assert not node_markdown.validate(old, 'streamer')[1]
    warnings, errors = compat.validate('streamer/主播.md', old.replace('156cm', '157cm'), node_markdown.PROFILE, baseline=old)
    assert not errors and node_compat.NOTICE in warnings
    _, errors = compat.validate('streamer/主播.md', old.replace('口癖: 原话', '口癖: 原话\n- 新节点'), node_markdown.PROFILE, baseline=old)
    assert errors and '草稿' in errors[0]


@pytest.mark.parametrize('target', [
    OLD.replace('## 说话风格', '## 特点'),
    OLD.replace('- 口癖: 原话', '- 口癖: 原话\n- 新节点'),
    OLD.replace('身高: 156cm', '生日: 156cm'),
    OLD.replace('本名: 主播', '本名: 另一个人'),
    OLD.replace('别名: 小主播', '别名: 新别名'),
    OLD.replace('口癖: 原话', 'Term|术语||新的行体'),
    OLD.replace('- 身高: 156cm', '- 身高: '),
    OLD.replace('最近更新日期: 2026-09-01', '最近更新日期: 2026-09-02'),
])
def test_unsafe_legacy_edits_are_rejected_with_draft_guidance(target):
    _, errors = compat.validate('streamer/主播.md', target, node_markdown.PROFILE, baseline=OLD)
    assert errors and '草稿' in errors[0]


@pytest.mark.parametrize('before,after', [('原话', '误听为“其他”'), ('误听为“旧变体”', '误听为“新变体”')])
def test_misheard_matching_edits_cannot_use_preservation(before, after):
    baseline = OLD.replace('口癖: 原话', '口癖: ' + before)
    _, errors = compat.validate('streamer/主播.md', baseline.replace(before, after), node_markdown.PROFILE, baseline=baseline)
    assert errors and '误听' in errors[0]


def test_export_baseline_tracks_prior_release_and_initial_source():
    a = client()
    upload(a, {'streamer/主播.md': OLD})
    assert a.put('/api/settings', json={'name': '旧知识兼容测试', 'profile': node_markdown.PROFILE}).status_code == 200
    entry = a.get('/api/entries').json()['items'][0]
    for value in ('157cm', '158cm'):
        target = OLD.replace('156cm', value)
        result = a.post('/api/entries/' + entry['entry_id'] + '/revisions', json={'base_revision': entry['id'], 'content': target})
        assert result.status_code == 200, result.text
        entry = result.json()
    approve(a, entry)
    release = publish(a, [entry])
    exported = zip_files(a, release)
    manifest = json.loads(exported['finesub-review-compat.json'])
    assert manifest['entries'][0]['baseline'] == OLD
    previous = exported['streamer/主播.md']
    result = a.post('/api/entries/' + entry['entry_id'] + '/revisions', json={'base_revision': entry['id'], 'content': previous.replace('158cm', '159cm')})
    assert result.status_code == 200, result.text
    entry = result.json()
    approve(a, entry)
    exported = zip_files(a, publish(a, [entry]))
    assert json.loads(exported['finesub-review-compat.json'])['entries'][0]['baseline'] == previous


def test_untrusted_bundle_cannot_create_old_nodes_in_current_profile():
    a = client()
    assert a.put('/api/settings', json={'name': '严格新增', 'profile': node_markdown.PROFILE}).status_code == 200
    files, report = compat.build_files({'streamer/主播.md': OLD}, node_markdown.PROFILE, baselines={'streamer/主播.md': OLD})
    assert not report['errors'] and 'finesub-review-compat.json' in files
    batch = upload(a, files, confirm=False)
    assert len(batch['items']) == 1 and batch['items'][0]['classification'] == 'invalid'
    response = a.post('/api/imports/' + batch['id'] + '/confirm', json={'item_ids': [batch['items'][0]['id']]})
    assert response.status_code == 422
    assert a.get('/api/entries').json()['total'] == 0
    assert a.get('/api/import-items/' + batch['items'][0]['id']).json()['content'] == OLD


@pytest.fixture(params=[node_markdown.PROFILE, node_markdown.CURRENT_PROFILE])
def native_config(request):
    names = ('REVIEW_KNOWLEDGE_TEST_ROOT', 'FINESUB_COMPAT_SRC', 'FINESUB_REFERENCE_SRC') if request.param == node_markdown.PROFILE else ('REVIEW_SCHEMA6_TEST_ROOT', 'FINESUB6_COMPAT_SRC', 'FINESUB6_REFERENCE_SRC')
    if not all(os.getenv(name) for name in names):
        pytest.skip('Supply explicit source and engines for ' + request.param)
    return request.param, *(os.environ[name] for name in names)


def test_cli_dry_run_apply_stale_and_unpatched_engine_are_safe(tmp_path, native_config):
    profile, knowledge_root, compat_src, reference_src = native_config
    source = Path(knowledge_root)
    docs = {name: (source / 'rendered' / name).read_text(encoding='utf-8') for name in FILES}
    targets = {**docs, FILES[0]: docs[FILES[0]].replace('标题「致並跡（ちへいせん）」=「地平線」同音借字', '标题「致並跡（ちへいせん）」=「地平線」同音借字（CLI副本验证）')}
    files, report = compat.build_files(targets, profile, baselines=docs)
    assert not report['errors']
    archive = tmp_path / 'cli-compat.zip'
    archive.write_bytes(compat.make_zip(files))
    copied = tmp_path / 'cli-copy'
    copied.mkdir()
    for name in ('knowledge.sqlite', 'knowledge.sqlite-wal'):
        if (source / name).exists():
            shutil.copy2(source / name, copied / name)
    base = [sys.executable, str(ROOT / 'scripts/apply_compat_release.py'), '--knowledge-root', str(copied), '--archive', str(archive)]
    before = compat.digest((copied / 'knowledge.sqlite').read_bytes())
    blocked = subprocess.run([*base, '--finesub-src', reference_src, '--apply'], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert blocked.returncode != 0 and '未安装' in blocked.stderr
    assert before == compat.digest((copied / 'knowledge.sqlite').read_bytes())
    dry = subprocess.run([*base, '--finesub-src', compat_src], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert dry.returncode == 0, dry.stdout + dry.stderr
    assert json.loads(dry.stdout)['dry_run'] and before == compat.digest((copied / 'knowledge.sqlite').read_bytes())
    applied = subprocess.run([*base, '--finesub-src', compat_src, '--apply'], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert applied.returncode == 0, applied.stdout + applied.stderr
    assert not json.loads(applied.stdout)['dry_run']
    after = compat.digest((copied / 'knowledge.sqlite').read_bytes())
    stale = subprocess.run([*base, '--finesub-src', compat_src, '--apply'], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert stale.returncode != 0 and '基础投影已变化' in stale.stderr
    assert after == compat.digest((copied / 'knowledge.sqlite').read_bytes())
    unrelated = next(path for path in (copied / 'rendered/common').glob('*.md') if path.name != 'index.md' and path.relative_to(copied / 'rendered').as_posix() not in FILES)
    pending = unrelated.read_text(encoding='utf-8') + '\n- 用户未收录的副本修改\n'
    unrelated.write_text(pending, encoding='utf-8')
    dirty = subprocess.run([*base, '--finesub-src', compat_src, '--apply'], capture_output=True, text=True, encoding='utf-8', timeout=30)
    assert dirty.returncode != 0 and '未收录修改' in dirty.stderr
    assert unrelated.read_text(encoding='utf-8') == pending
    assert after == compat.digest((copied / 'knowledge.sqlite').read_bytes())


def test_three_real_samples_roundtrip_and_native_invariants(tmp_path, native_config):
    profile, knowledge_root, compat_src, _ = native_config
    source = Path(knowledge_root)
    docs = {name: (source / 'rendered' / name).read_text(encoding='utf-8') for name in FILES}
    imports = dict(docs)
    for category in ('common', 'streamer'):
        index = source / 'rendered' / category / 'index.md'
        if index.exists():
            imports[category + '/index.md'] = index.read_text(encoding='utf-8')
    a = client()
    upload(a, imports)  # Administrator explicitly establishes the old source once.
    assert a.put('/api/settings', json={'name': '三份真实旧知识', 'profile': profile}).status_code == 200
    original = {entry['path']: entry for entry in a.get('/api/entries').json()['items']}
    targets = {
        FILES[0]: docs[FILES[0]].replace('标题「致並跡（ちへいせん）」=「地平線」同音借字', '标题「致並跡（ちへいせん）」=「地平線」同音借字（副本兼容验证）'),
        FILES[1]: docs[FILES[1]].replace('拍摄三天两夜：前两天京都，最后一天滋贺县', '拍摄三天两夜：前两天京都，最后一天滋贺县（副本兼容验证）'),
        FILES[2]: docs[FILES[2]].replace('身高: 156cm', '身高: 156cm（副本兼容验证）'),
    }
    revised = []
    for name in FILES:
        entry = original[name]
        assert targets[name] != docs[name]
        detail = a.get('/api/entries/' + entry['entry_id']).json()
        assert not detail['errors'] and node_compat.NOTICE in detail['warnings']
        for target in (docs[name], docs[name].replace(node_markdown.subject_intro(docs[name]), node_markdown.subject_intro(docs[name]) + '（简介副本验证）', 1), targets[name]):
            response = a.post('/api/entries/' + entry['entry_id'] + '/revisions', json={'base_revision': entry['id'], 'content': target})
            assert response.status_code == 200, response.text
            entry = response.json()
        stale = a.post('/api/entries/' + entry['entry_id'] + '/revisions', json={'base_revision': original[name]['id'], 'content': docs[name]})
        assert stale.status_code == 409
        unsafe = a.post('/api/entries/' + entry['entry_id'] + '/revisions', json={'base_revision': entry['id'], 'content': targets[name].replace('## 元数据', '## 新章节\n- 新节点\n\n## 元数据')})
        assert unsafe.status_code == 422 and '草稿' in unsafe.text
        assert a.get('/api/entries/' + entry['entry_id']).json()['entry']['content'] == targets[name]
        approve(a, entry)
        revised.append(entry)
    release = publish(a, revised)
    exported = zip_files(a, release)
    for name in FILES:
        assert exported[name] == targets[name]
    manifest = json.loads(exported['finesub-review-compat.json'])
    assert {row['path']: row['baseline'] for row in manifest['entries']} == docs
    reimport = upload(a, exported, confirm=False)
    assert all(not item['error'] and item['classification'] == 'duplicate' for item in reimport['items'])
    archive = tmp_path / 'preserved-export.zip'
    archive.write_bytes(a.get('/api/releases/' + release + '/download').content)
    (ROOT / 'test-results' / f'preserved-export-{profile}.zip').write_bytes(archive.read_bytes())
    copied = tmp_path / 'native-copy'
    copied.mkdir()
    hashes_before = {name: compat.digest((source / name).read_bytes()) if (source / name).exists() else None for name in ('knowledge.sqlite', 'knowledge.sqlite-wal')}
    for name in hashes_before:
        if hashes_before[name]:
            shutil.copy2(source / name, copied / name)
    assert hashes_before == {name: compat.digest((source / name).read_bytes()) if (source / name).exists() else None for name in hashes_before}
    report_path = tmp_path / 'native-preserved.json'
    command = [sys.executable, str(ROOT / 'backend/tests/native_preserved_probe.py'), '--engine-src', compat_src, '--knowledge-root', str(copied), '--archive', str(archive), '--report', str(report_path)]
    probe = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=60)
    assert probe.returncode == 0, probe.stdout + probe.stderr
    report = json.loads(report_path.read_text(encoding='utf-8'))
    assert report['counts'] == [[6, 6], [10, 10], [7, 7]]
    assert report['original_node_ids_and_kinds_preserved'] and report['memberships_and_matching_items_unchanged']
    assert report['unrelated_payloads_unchanged'] and report['evidence_history_preserved'] and report['atomic_conflict_preserved_other_fields']
    (ROOT / 'test-results' / f'native-preserved-{profile}.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    assert hashes_before == {name: compat.digest((source / name).read_bytes()) if (source / name).exists() else None for name in hashes_before}
