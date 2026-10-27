"""Current FineSub projections through the actual review and release workflow."""
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

from app import compat, node_markdown
from app.worker import run_once
from test_workflow import approve, client, isolated_database, publish, upload, zip_files

ROOT = Path(__file__).resolve().parents[2]
COMMON = '# 作品\n官方作品名。\n\n## 档案\n- [本名] 作品 / Work\n- [别名] 作品别称\n- [收录范围] 固定角色名。\n\n## 角色\n- [主人公] Hero|英雄|主角别称|主人公：描述含|竖线。误听为“ヒロ”。\n\n## 元数据\n最近更新日期: 2026-10-01\n'
STREAMER = '# 主播\n节目主持人。\n\n## 档案\n<!-- 原生完整预览的格式说明\n     [示例] 不应成为知识 -->\n- [本名] 主播 / Broadcaster\n- [别名] 主播别称\n- [人设] 节目主持人\n- [外观]\n\n## 直播内容\n- 原话：冒号仍是正文。\n\n## 频道用语\n- [问候语] Hello|你好||开场问候\n\n## 人际关系\n- Friend|朋友|好友|节目共演者\n\n## 待归类\n- [自定义] 待核实的语境。\n\n## 元数据\n最近更新日期: 2026-10-01\n'
STYLE = '# 团队风格\n团队字幕约定。\n\n## 档案\n- [本名] 团队风格\n- [作者] 字幕组\n\n## 约定\n- [标点] 保留说话节奏。\n\n## 正例\n- [标点] 原话 | 多人台词 → 译文：停顿。\n\n## 反例\n- [标点] 原话 → 译文不停顿。\n\n## 元数据\n最近更新日期: 2026-10-01\n'


def current_profile(c):
    response = c.put('/api/settings', json={'name': '隔离节点验收', 'profile': node_markdown.PROFILE})
    assert response.status_code == 200, response.text


@pytest.mark.parametrize('category,body,valid', [
    ('common', '## 角色\nHero|英雄||描述: 含|竖线', True),
    ('common', '## 角色\nHero|英雄|缺少别名列', False),
    ('streamer', '## 人际关系\n朋友: 共演者', False),
    ('streamer', '## 人际关系\nFriend|朋友||共演者', True),
    ('streamer', '## 档案\n- [未知标记] 没有登记仍可保留\n- [外观]', True),
    ('streamer', '## 未知章节\n不能静默删除', False),
    ('style', '## 约定\n- [A] 规则\n## 正例\n- [B] 孤立例子', False),
    ('style', '## 约定\n- [A] 规则\n- [ａ] 重复标记', False),
    ('style', '## 正例\n无标记示例 | 多人台词 | 竖线不是术语', True),
    ('common', '## 角色\n- [] Hero|英雄||描述', False),
])
def test_current_line_and_section_admission(category, body, valid):
    _, errors = compat.validate(f'{category}/测试.md', '# 测试\n简介\n\n' + body + '\n', node_markdown.PROFILE)
    assert bool(errors) is not valid, errors


def test_comment_slots_byte_limit_and_style_caps_are_not_silently_ignored():
    _, errors = compat.validate('translation/featured.md', '# 翻译纠错精选\n', node_markdown.PROFILE)
    assert any('不再读取' in e for e in errors)
    _, errors = compat.validate('streamer/主播.md', STREAMER.replace('-->', ''), node_markdown.PROFILE)
    assert any('注释未闭合' in e for e in errors)
    _, errors = compat.validate('style/测试.md', '# 测试\n## 约定\n' + '\n'.join(f'[规则{i}] 内容' for i in range(21)), node_markdown.PROFILE)
    assert any('20 行' in e for e in errors)
    _, errors = compat.validate('style/测试.md', '# 测试\n## 约定\n[规则] ' + '字' * 200, node_markdown.PROFILE)
    assert any('200 字' in e for e in errors)
    _, errors = compat.validate('streamer/测试.md', '# 测试\n## 直播内容\n' + '字' * 333_334, node_markdown.PROFILE)
    assert any('UTF-8 字节' in e for e in errors)


def test_projection_selection_is_scoped_and_sqlite_never_falls_back_to_archives():
    files = [('包/knowledge.sqlite', None), ('包/common/作品.md', b'old'), ('包/rendered/common/作品.md', COMMON.encode()), ('别的包/common/其他.md', b'keep')]
    selected, warnings = compat.select_sources(files)
    assert '包/common/作品.md' not in dict(selected)
    assert dict(selected)['别的包/common/其他.md'] == b'keep'
    assert warnings and '历史档案' in warnings[0]
    with pytest.raises(ValueError, match='当前知识源'):
        compat.select_sources([('包/knowledge.sqlite', None), ('包/common/作品.md', b'old')])


def test_current_import_revision_decision_release_preserves_projection_and_index():
    a = client()
    current_profile(a)
    sources = {'包/knowledge.sqlite': b'not opened', '包/common/作品.md': '# 过期档案',
               '包/rendered/common/作品.md': COMMON, '包/rendered/streamer/主播.md': STREAMER,
               '包/rendered/style/团队风格.md': STYLE,
               '包/rendered/common/index.md': '- 作品 [动画] | Work | 作品别称 | 官方作品名。\n',
               '包/bundle.json': '{"schema":1,"nodes":[]}'}
    batch = upload(a, sources)
    assert len(batch['items']) == 3 and not [i for i in batch['items'] if i['error']]
    assert any('bundle.json' in w for w in batch['warnings'])
    entries = a.get('/api/entries?limit=100').json()['items']
    common = next(e for e in entries if e['category'] == 'common')
    assert common['kind'] == '动画' and common['aliases'] == '作品别称'
    assert a.get('/api/entries?q=作品别称').json()['total'] == 1
    changed = COMMON.replace('官方作品名。', '人工核实后的官方作品名。')
    revised = a.post(f'/api/entries/{common["entry_id"]}/revisions', json={'base_revision': common['id'], 'content': changed})
    assert revised.status_code == 200 and revised.json()['kind'] == '动画'
    assert a.post(f'/api/entries/{common["entry_id"]}/decision', json={'revision_id': common['id'], 'status': 'approved', 'reason': '旧版本'}).status_code == 409
    entries = a.get('/api/entries?limit=100').json()['items']
    for entry in entries:
        approve(a, entry)
    release = publish(a, entries)
    files = zip_files(a, release)
    assert files['common/作品.md'] == changed
    assert files['streamer/主播.md'] == STREAMER and files['style/团队风格.md'] == STYLE
    assert '作品 [动画] | Work | 作品别称' in files['common/index.md']
    assert 'Broadcaster | 主播别称' in files['streamer/index.md']
    assert 'style/index.md' not in files and 'bundle.json' not in files
    (ROOT / 'test-results' / 'node-export.zip').write_bytes(a.get(f'/api/releases/{release}/download').content)


def test_profile_changes_are_revalidated_and_conflicts_preserve_the_base():
    a = client()
    batch = upload(a, {'streamer/主播.md': STREAMER.replace('Friend|朋友|好友|节目共演者', '朋友: 共演者')}, confirm=False)
    current_profile(a)
    response = a.post(f'/api/imports/{batch["id"]}/confirm', json={'item_ids': [batch['items'][0]['id']]})
    assert response.status_code == 422
    upload(a, {'common/作品.md': COMMON})
    entry = a.get('/api/entries').json()['items'][0]
    upload(a, {'style/团队风格.md': STYLE})  # unrelated work must not invalidate the entry CAS
    valid = COMMON.replace('官方作品名。', '更新简介。')
    response = a.post(f'/api/entries/{entry["entry_id"]}/revisions', json={'base_revision': entry['id'], 'content': valid})
    assert response.status_code == 200
    stale = a.post(f'/api/entries/{entry["entry_id"]}/revisions', json={'base_revision': entry['id'], 'content': COMMON})
    assert stale.status_code == 409
    assert a.get(f'/api/entries/{entry["entry_id"]}').json()['entry']['content'] == valid


def test_invalid_current_documents_report_each_error_and_preserve_originals():
    a = client()
    current_profile(a)
    bad = {'common/作品.md': COMMON.replace('Hero|英雄|主角别称|主人公：描述含|竖线。误听为“ヒロ”。', 'Hero|英雄|只有三列'),
           'streamer/主播.md': STREAMER.replace('Friend|朋友|好友|节目共演者', '朋友: 共演者')}
    batch = upload(a, {**bad, 'style/团队风格.md': STYLE}, confirm=False)
    assert batch['status'] == 'preview'
    for item in batch['items']:
        preview = a.get('/api/import-items/' + item['id']).json()
        if item['path'] in bad:
            assert item['error'] and item['classification'] == 'invalid'
            assert preview['content'] == bad[item['path']]
        else:
            assert not item['error']
    asset = batch['assets'][0]
    original = a.get('/api/assets/' + asset['id'] + '/download')
    assert original.status_code == 200
    import io, zipfile
    with zipfile.ZipFile(io.BytesIO(original.content)) as archive:
        assert all(archive.read(path).decode('utf-8') == body for path, body in bad.items())
    valid_ids = [item['id'] for item in batch['items'] if not item['error']]
    response = a.post('/api/imports/' + batch['id'] + '/confirm', json={'item_ids': valid_ids})
    assert response.status_code == 200 and response.json()['created'] == 1


def test_style_namespace_is_not_used_for_name_alias_overlap():
    a = client()
    current_profile(a)
    upload(a, {'common/作品.md': COMMON, 'style/作品别称.md': STYLE.replace('团队风格', '作品别称')})
    entries = a.get('/api/entries').json()['items']
    common = next(e for e in entries if e['category'] == 'common')
    assert not any('别名' in w for w in a.get(f'/api/entries/{common["entry_id"]}').json()['warnings'])


def test_current_release_rejects_duplicate_matchable_names_but_style_is_separate():
    files = {'common/主播.md': COMMON.replace('作品', '主播'), 'streamer/主播.md': STREAMER}
    _, report = compat.build_files(files, node_markdown.PROFILE)
    assert any('跨分类重复' in e for e in report['errors'])
    files.pop('streamer/主播.md')
    files['style/主播.md'] = STYLE.replace('团队风格', '主播')
    _, report = compat.build_files(files, node_markdown.PROFILE)
    assert not report['errors']


@pytest.mark.parametrize('profile', ['legacy-preserve', node_markdown.PROFILE])
def test_style_body_has_no_matching_index_and_existing_common_type_is_preserved(profile):
    files, report = compat.build_files({'common/作品.md': COMMON, 'style/团队风格.md': STYLE}, profile,
                                       kind_hints={'common/作品.md': '动画'})
    assert not report['errors']
    assert files['style/团队风格.md'] == STYLE
    assert 'style/index.md' not in files
    assert '作品 [动画] | Work | 作品别称' in files['common/index.md']


def test_native_identity_keeps_spaces_but_casefolds_keys_and_labels():
    bodies = {'common/A B.md': '# A B\n简介\n', 'streamer/AB.md': '# AB\n简介\n'}
    _, report = compat.build_files(bodies, node_markdown.PROFILE)
    assert not report['errors']
    bodies = {'common/Straße.md': '# Straße\n简介\n', 'streamer/STRASSE.md': '# STRASSE\n简介\n'}
    _, report = compat.build_files(bodies, node_markdown.PROFILE)
    assert any('跨分类重复' in error for error in report['errors'])
    _, errors = compat.validate('style/测试.md', '# 测试\n## 约定\n[A B] 规则\n## 正例\n[AB] 无对应规则\n', node_markdown.PROFILE)
    assert any('没有对应约定' in error for error in errors)


def test_invalid_index_does_not_block_unrelated_valid_import_and_intro_is_not_truncated():
    a = client()
    current_profile(a)
    body = COMMON.replace('官方作品名。', '长简介' * 150)
    batch = upload(a, {'common/作品.md': body, 'common/index.md': b'\xff'})
    assert any(i['error'] for i in batch['items'])
    entry = a.get('/api/entries').json()['items'][0]
    approve(a, entry)
    files = zip_files(a, publish(a, [entry]))
    assert '长简介' * 150 in files['common/index.md']


def test_jsonl_create_uses_current_profile_labels_and_group_rules():
    a = client()
    current_profile(a)
    proposal = {'category': 'common', 'entry': '作品', 'op': 'create_entry', 'entry_type': '动画', 'intro': '作品简介',
                'aliases': ['作品别称'], 'reason': '这个独立作品有自己的固定术语，需要建立独立条目。'}
    batch = upload(a, {'proposal': json.dumps(proposal, ensure_ascii=False)}, filename='current.jsonl')
    assert not batch['items'][0]['error']
    entry = a.get('/api/entries').json()['items'][0]
    assert entry['kind'] == '动画' and entry['aliases'] == '作品别称'
    assert '[别名] 作品别称' in a.get(f'/api/entries/{entry["entry_id"]}').json()['entry']['content']


@pytest.mark.skipif(not os.getenv('FINESUB_REFERENCE_SRC'), reason='Supply explicit native FineSub source')
def test_native_schema5_write_read_and_partial_rejection_is_a_failure(tmp_path):
    source = os.environ['FINESUB_REFERENCE_SRC']
    archive = tmp_path / 'nodes.zip'
    files, report = compat.build_files({'common/作品.md': COMMON, 'streamer/主播.md': STREAMER, 'style/团队风格.md': STYLE}, node_markdown.PROFILE)
    assert not report['errors']
    archive.write_bytes(compat.make_zip(files))
    command = [sys.executable, str(ROOT / 'scripts' / 'verify_finesub.py'), '--finesub-src', source, '--archive', str(archive), '--profile', node_markdown.PROFILE]
    passed = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=45)
    assert passed.returncode == 0, passed.stdout + passed.stderr
    result = json.loads(passed.stdout)
    assert result['finesub_version'] == '0.5.1' and result['schema'] == 5 and result['subjects'] == 3 and result['items'] >= 4
    files['common/作品.md'] = COMMON.replace('[别名] 作品别称', '[别名] Alice、Ａｌｉｃｅ')
    archive.write_bytes(compat.make_zip(files))
    rejected = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=45)
    assert rejected.returncode != 0 and 'partial rejection' in rejected.stderr
    files['unrecognized.json'] = '{}'
    archive.write_bytes(compat.make_zip(files))
    rejected = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=45)
    assert rejected.returncode != 0 and 'Unsupported release content' in rejected.stderr
