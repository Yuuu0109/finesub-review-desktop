"""Plan existing-node value updates using the native store and apply engine.

Needs the matching engine-preserved-values patch for body edits. Does not migrate kinds,
sections, matching items or visibility and never writes SQL directly.
"""
import json
import sys
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'backend'))
from app import compat, node_compat, node_markdown

ENGINE_BASE = 'af32b0145f70a98199b246704076c384bfda95ee'


def engine_profile():
    from finesub.llm.knowledge.node.schema import SCHEMA_VERSION
    return node_markdown.profile_for_schema(SCHEMA_VERSION)


def check_engine(profile=None):
    from finesub.llm.knowledge.node import apply as native_apply
    actual = engine_profile()
    if profile is not None and profile != actual:
        raise ValueError(f'发布包 {profile} 与引擎 {actual} 不匹配；未写入，不自动迁移')
    spec = node_markdown.NODE_PROFILES[actual]
    if getattr(native_apply, 'PRESERVED_ENGINE_BASE', None) != spec['rules']['commit'] or getattr(native_apply, 'PRESERVED_VALUES_COMPAT_VERSION', None) != 1:
        raise ValueError(f'指定引擎未安装此版本的受限兼容补丁；未写入。请先审阅 {spec["patch"]}，在副本验证后另行落地引擎补丁')
    return native_apply


def value_line(line):
    # The legacy archive omitted the human renderer's bullet. This is only
    # display normalization: labels, values and row order remain exact.
    return line[2:] if line.startswith('- ') else line


def fingerprint(content):
    from finesub.llm.knowledge.node.importer import EMPTY_SLOT_RE
    parsed = node_compat.projection(content)
    parsed['sections'] = [
        {'name': s['name'], 'lines': [value_line(line) for line in s['lines']
                                    if not EMPTY_SLOT_RE.fullmatch(value_line(line).strip())]}
        for s in parsed['sections'] if s['name'] != '元数据'
    ]
    return compat.digest(json.dumps(parsed, ensure_ascii=False, sort_keys=True))


def plan_subject(repo, subject, baseline, target):
    from finesub.llm.knowledge.node.envelope import Binding, Envelope
    from finesub.llm.knowledge.node.render import format_line, node_aliases
    from finesub.llm.knowledge.node.apply import PRESERVED_VALUE_FIELDS

    category = subject.payload['category']
    before, after, edits = node_compat.preserving_changes(baseline, target, category, engine_profile())
    if fingerprint(repo.entry_text(subject.local_id)) != fingerprint(baseline):
        raise ValueError('原生基础投影已变化，请重新导出合并；不会覆盖知识库')
    nodes = {}
    for membership in repo.store.children(subject.local_id, repo.rev):
        child = repo.store.node(membership.child_id, repo.rev)
        formatted = format_line(child, aliases=node_aliases(repo.store, child, repo.rev))
        human = formatted if formatted.startswith('- ') else '- ' + formatted
        nodes.setdefault((membership.section, value_line(human)), []).append(child)
    bindings = []
    operations = []
    changed_ids = []
    for edit in edits:
        matches = nodes.get((edit['section'], value_line(edit['before'])), [])
        if len(matches) != 1:
            raise ValueError(f'「{edit["section"]}」无法唯一定位旧节点；保留原内容与草稿')
        child = matches[0]
        if len(repo.store.parents(child.local_id, repo.rev)) != 1:
            raise ValueError('共享节点的跨主体编辑需要单独审核；本工具不修改其他主体')
        field = PRESERVED_VALUE_FIELDS.get(child.kind)
        old_value = child.payload.get(field) if field else None
        if not isinstance(old_value, str) or not old_value or not edit['before'].endswith(old_value):
            raise ValueError('旧节点无法无损映射到单一值字段，保留原内容与草稿')
        prefix = edit['before'][:-len(old_value)]
        if not edit['after'].startswith(prefix):
            raise ValueError('编辑改变了旧节点的身份或格式前缀')
        value = edit['after'][len(prefix):]
        if not value.strip():
            raise ValueError('不能清空旧节点值')
        handle = '@value' + str(len(bindings) + 1)
        bindings.append(Binding(handle, 'node', child.local_id, child.valid_from_rev))
        operations.append({'op': 'update_preserved_value', 'id': handle, 'value': value})
        changed_ids.append(child.local_id)
    if edits or before['intro'] != after['intro']:
        handle = '@subject'
        bindings.append(Binding(handle, 'node', subject.local_id, subject.valid_from_rev))
        changes = {'payload.updated_date': repo.today()}
        if before['intro'] != after['intro']:
            changes['payload.intro'] = after['intro']
        operations.append({'op': 'update', 'id': handle, 'set': changes})
    return Envelope(task_id='review-preserved-values', assignment_id='review-compat', context_epoch=0,
                    knowledge_read_rev=repo.rev, ops=operations, handle_bindings=bindings), changed_ids


def apply_subject(repo, subject, baseline, target, *, write=False):
    return apply_release(repo, [(subject, baseline, target)], write=write)


def apply_release(repo, tasks, *, write=False):
    native_apply = check_engine()
    from finesub.llm.knowledge.node.envelope import Binding, Envelope
    operations, bindings, changed_ids = [], [], []
    for number, (subject, baseline, target) in enumerate(tasks):
        part, ids = plan_subject(repo, subject, baseline, target)
        handles = {binding.handle: '@part' + str(number) + '_' + binding.handle[1:] for binding in part.handle_bindings}
        bindings.extend(Binding(handles[b.handle], b.kind, b.id, b.expected_valid_from_rev) for b in part.handle_bindings)
        operations.extend({**op, 'id': handles[op['id']]} for op in part.ops)
        changed_ids.extend(ids)
    envelope = Envelope(task_id='review-preserved-values', assignment_id='review-compat', context_epoch=0,
                        knowledge_read_rev=repo.rev, ops=operations, handle_bindings=bindings)
    overlay, problems = native_apply.preview(repo.store, envelope)
    if problems or overlay.rejected:
        raise ValueError('原生兼容预检拒绝：' + str(problems or overlay.rejected))
    if not write:
        return {'dry_run': True, 'changed_nodes': changed_ids, 'operations': len(envelope.ops)}
    result = native_apply.apply_envelope(repo.store, envelope, revision_kind='user', note='review preserved values', atomic=True)
    if result.rolled_back or result.rejected_ops or result.conflicts:
        raise ValueError('兼容写入拒绝：' + str(result))
    return {'dry_run': False, 'changed_nodes': changed_ids, 'rev': result.rev, 'operations': len(envelope.ops)}


def read_archive(path, *, expected_profile=None):
    with zipfile.ZipFile(path) as archive:
        members = archive.infolist()
        if len(members) > compat.MAX_FILES or sum(m.file_size for m in members) > compat.MAX_TOTAL:
            raise ValueError('兼容包超过导入限制')
        names = [compat.safe_path(m.filename) for m in members]
        if len(set(n.casefold() for n in names)) != len(names):
            raise ValueError('兼容包有重复路径')
        manifest = json.loads(archive.read('finesub-review-compat.json'))
        if not isinstance(manifest, dict) or manifest.get('mode') != 'preserve-existing-values':
            raise ValueError('不支持的兼容包模式')
        if manifest.get('schema') == 1 and manifest.get('engine_reference') == 'Yuuu0109/nonoka-sub-x@' + ENGINE_BASE:
            profile = node_markdown.PROFILE  # Historical packages only targeted schema 5.
        elif manifest.get('schema') == 2:
            profile = node_markdown.validate_release_profile({key: (1 if key == 'schema' else manifest.get(key))
                for key in ('schema', 'profile', 'knowledge_schema', 'engine_reference')})
        else:
            raise ValueError('不支持的兼容包模式')
        if 'finesub-review-profile.json' in names:
            declared = node_markdown.validate_release_profile(json.loads(archive.read('finesub-review-profile.json')))
            if declared != profile:
                raise ValueError('发布包和兼容清单的知识库版本不一致')
        if expected_profile is not None and expected_profile != profile:
            raise ValueError(f'发布包 {profile} 与引擎 {expected_profile} 不匹配；未写入，不自动迁移')
        if not isinstance(manifest.get('entries'), list) or not manifest['entries']:
            raise ValueError('兼容包没有有效条目')
        rows = []
        seen = set()
        for entry in manifest['entries']:
            name = compat.safe_path(entry['path'])
            if name in seen or name.split('/')[0] not in ('common', 'streamer') or len(name.split('/')) != 2:
                raise ValueError('无效或重复兼容条目')
            seen.add(name)
            if archive.getinfo(name).file_size > compat.MAX_FILE or len(entry['baseline'].encode('utf-8')) > compat.MAX_FILE:
                raise ValueError('兼容条目超过单文件限制')
            target = compat.text_decode(archive.read(name))
            if compat.digest(target) != entry['target_sha256'] or compat.digest(entry['baseline']) != entry['baseline_sha256']:
                raise ValueError('兼容包哈希不符')
            if node_compat.projection(entry['baseline'])['title'] != '# ' + Path(name).stem:
                raise ValueError('兼容基线主体身份不符')
            rows.append((name, entry['baseline'], target))
        return rows
