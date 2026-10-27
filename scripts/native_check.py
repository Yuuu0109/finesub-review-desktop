"""Native FineSub verification using temporary stores; never writes back."""
import argparse
import json
import re
import sys
import tempfile
import uuid
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'backend'))
from app import compat
from app import node_markdown


def verify_nodes(root, profile):
    from finesub.llm.knowledge.base import _parse_entry_for_index, parse_index_text
    from finesub.llm.knowledge.node.edit import edit_subject
    from finesub.llm.knowledge.node.importer import EMPTY_SLOT_RE, classify_line, extract_misheard, parse_entry_text, storable_term_payload
    from finesub.llm.knowledge.node.presets import preset_for_category
    from finesub.llm.knowledge.node.repo import KnowledgeRepo
    from finesub.llm.knowledge.node.schema import SCHEMA_VERSION

    expected_schema = node_markdown.NODE_PROFILES[profile]['schema']
    if SCHEMA_VERSION != expected_schema:
        raise ValueError(f'{profile} 要求 schema {expected_schema}，实际引擎为 {SCHEMA_VERSION}')
    documents = {f'{c}/{p.name}': p.read_text(encoding='utf-8') for c in node_markdown.CATEGORIES for p in (root / c).glob('*.md') if p.name != 'index.md'}
    _, validation = compat.build_files(documents, profile)
    if validation['errors']:
        raise ValueError(str(validation['errors']))
    repo = KnowledgeRepo.open(root / 'native-check', auto_import=False)
    report = {'schema': expected_schema, 'subjects': 0, 'nodes': 0, 'items': 0}
    try:
        for category in node_markdown.CATEGORIES:
            index_path = root / category / 'index.md'
            index = {e.key: e for e in parse_index_text(index_path.read_text(encoding='utf-8'))} if index_path.exists() else {}
            count = 0
            for path in sorted((root / category).glob('*.md')):
                if path.name == 'index.md':
                    continue
                text = path.read_text(encoding='utf-8')
                _, errors = compat.validate(f'{category}/{path.name}', text, profile)
                if errors:
                    raise ValueError(f'{path.name}: {errors}')
                parsed = parse_entry_text(path, text)
                subject_id = str(uuid.uuid4())
                with repo.store.begin('user', note='isolated review export verification') as tx:
                    subject = tx.create_node(subject_id, 'subject', {
                        'surface': parsed.h1, 'intro': '', 'category': category,
                        'entry_type': index[parsed.h1].entry_type if parsed.h1 in index else '',
                        **({'recategorized_reason': parsed.recategorized_reason} if getattr(parsed, 'recategorized_reason', '') else {}),
                    })
                applied = edit_subject(repo, subject, text, refresh=False)
                if applied.rejected or applied.staged:
                    raise ValueError(f'{path.name}: native partial rejection: {applied.rejected or applied.staged}')
                preset = preset_for_category(category)
                alias_role = preset.label_by_role('aliases')
                expected, actual = {}, {}
                for section in parsed.sections:
                    if section.name == '元数据':
                        continue
                    spec = preset.section(section.name)
                    expected[section.name] = []
                    for _, raw, _ in section.lines:
                        body = re.sub(r'^\s*- ', '', raw, count=1)
                        if not body.strip() or EMPTY_SLOT_RE.match(body):
                            continue
                        if alias_role and section.name == alias_role[0] and body.startswith(f'[{alias_role[1].name}]'):
                            continue
                        kind, payload = classify_line(body, spec, preset=preset)
                        if kind == 'invalid':
                            raise ValueError(f'{path.name}: unparseable native line {body!r}')
                        aliases = []
                        if kind == 'term':
                            payload, aliases = storable_term_payload(payload)
                        expected[section.name].append((kind, payload, sorted(set(aliases)), sorted(set(extract_misheard(body)))))
                for membership in repo.store.children(subject_id):
                    child = repo.store.node(membership.child_id)
                    items = repo.store.items_of(child.local_id)
                    actual.setdefault(membership.section, []).append((child.kind, child.payload,
                        sorted({i.value for i in items if i.field == 'aliases'}),
                        sorted({i.value for i in items if i.field == 'misheard'})))
                    report['nodes'] += 1
                    report['items'] += len(items)
                if {k: v for k, v in expected.items() if v} != actual:
                    raise ValueError(f'{path.name}: semantic content lost during native write/read')
                subject = repo.store.node(subject_id)
                _, _, native, aliases = _parse_entry_for_index(text)
                stored_aliases = sorted(i.value for i in repo.store.items_of(subject_id) if i.field == 'aliases')
                if subject.payload.get('intro', '') != parsed.intro or subject.payload.get('native_names', []) != list(native) or stored_aliases != sorted(set(aliases)):
                    raise ValueError(f'{path.name}: native subject metadata differs')
                if profile == node_markdown.CURRENT_PROFILE and subject.payload.get('recategorized_reason', '') != parsed.recategorized_reason:
                    raise ValueError(f'{path.name}: native recategorization metadata differs')
                rendered = repo.entry_text(subject_id)
                repeat = edit_subject(repo, subject, rendered, refresh=False)
                if repeat.changed or repeat.rejected:
                    raise ValueError(f'{path.name}: rendered text is not an idempotent native edit')
                if category == 'style':
                    resolved = repo.resolve(parsed.h1)
                    if resolved is not None and resolved.subject_id == subject_id:
                        raise ValueError('style incorrectly participates in name matching')
                count += 1
                report['subjects'] += 1
                report['items'] += len(repo.store.items_of(subject_id))
            report[category] = count
            if category == 'style' and index_path.exists():
                raise ValueError('style has no native matching index; export style bodies without index.md')
            if category in ('common', 'streamer') and index_path.exists():
                native_index = {e.key: e for e in repo.index_entries(category)}
                if index != native_index:
                    raise ValueError(f'{category}: index metadata differs from native subject data')
        stored_schema = repo.store.conn.execute("SELECT value FROM meta WHERE key='schema_version'").fetchone()[0]
        if stored_schema != str(expected_schema):
            raise ValueError(f'Native store did not use schema {expected_schema}')
        return report
    finally:
        KnowledgeRepo.forget(root / 'native-check')


def verify_archive(archive_path, profile):
    from finesub.llm.knowledge.node.repo import KnowledgeRepo
    from finesub.llm.knowledge.node.schema import SCHEMA_VERSION

    with tempfile.TemporaryDirectory(prefix='finesub-review-verify-') as tmp:
        root = Path(tmp)
        declared_profile = None
        with zipfile.ZipFile(archive_path) as archive:
            seen = set()
            if len(archive.infolist()) > compat.MAX_FILES or sum(i.file_size for i in archive.infolist()) > compat.MAX_TOTAL:
                raise ValueError('Archive exceeds import limits')
            for item in archive.infolist():
                name = compat.safe_path(item.filename)
                if name.casefold() in seen:
                    raise ValueError(f'Duplicate release path: {name}')
                seen.add(name.casefold())
                if item.is_dir():
                    continue
                if name == 'finesub-review-profile.json':
                    if item.file_size > compat.MAX_FILE:
                        raise ValueError('Archive exceeds import limits')
                    declared_profile = node_markdown.validate_release_profile(json.loads(archive.read(item)))
                    continue
                if name == 'finesub-review-compat.json':
                    raise ValueError('此包含旧知识定点兼容修改，不能用严格文本导入器重建旧节点；请用 apply_compat_release.py 对已有知识库副本 dry-run，并使用指定受限引擎补丁')
                if item.file_size > compat.MAX_FILE:
                    raise ValueError('Archive exceeds import limits')
                if not name.endswith('.md') or name.split('/')[0] not in (*node_markdown.CATEGORIES, 'translation'):
                    raise ValueError(f'Unsupported release content: {name}')
                if name.split('/')[0] in node_markdown.CATEGORIES and len(name.split('/')) != 2:
                    raise ValueError(f'Unsupported release content: {name}')
                if name.startswith('translation/') and name not in ('translation/common-mistake.md', 'translation/good-example.md'):
                    raise ValueError(f'Unsupported release content: {name}')
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.read(item))
        if declared_profile:
            if profile != 'auto' and profile != declared_profile:
                raise ValueError('指定规则与发布包的知识库版本不一致')
            profile = declared_profile
        if profile == 'auto':
            modern = any(compat.is_node_markdown(p.read_text(encoding='utf-8')) for c in node_markdown.CATEGORIES for p in (root / c).glob('*.md') if p.name != 'index.md')
            profile = node_markdown.profile_for_schema(SCHEMA_VERSION) if modern or (root / 'style').exists() else 'legacy-preserve'
        if profile in node_markdown.NODE_PROFILES:
            report = verify_nodes(root, profile)
        else:
            if (root / 'style').exists():
                raise ValueError('style requires current node verification; it cannot be skipped in legacy mode')
            repo = KnowledgeRepo.open(root)
            try:
                report = {}
                for category in ('common', 'streamer'):
                    entries = repo.index_entries(category)
                    expected = [p for p in (root / category).glob('*.md') if p.name != 'index.md']
                    if len(entries) != len(expected) or any(not (root / category / f'{e.key}.md').exists() for e in entries):
                        raise ValueError(f'{category}: native legacy import differs from release')
                    report[category] = len(entries)
                report['schema'] = int(repo.store.conn.execute("SELECT value FROM meta WHERE key='schema_version'").fetchone()[0])
            finally:
                KnowledgeRepo.forget(root)
        mistakes = root / 'translation' / 'common-mistake.md'
        examples = root / 'translation' / 'good-example.md'
        if mistakes.exists() or examples.exists():
            try:
                from finesub.llm.knowledge.mistakes import parse_common_mistakes, parse_good_examples
            except ModuleNotFoundError as exc:
                raise ValueError('所指定的 FineSub 已移除旧 translation 读取器；旧归档被保留，但不能报告为当前原生读取通过') from exc
        if mistakes.exists():
            records, featured = parse_common_mistakes(mistakes.read_text(encoding='utf-8'))
            if len(featured) > 30 or not set(featured).issubset({r.id for r in records}):
                raise ValueError('Invalid featured translation references')
            report.update(mistakes=len(records), featured=len(featured))
        if examples.exists():
            report['examples'] = len(parse_good_examples(examples.read_text(encoding='utf-8')))
        return {'native_finesub_reader': 'passed', 'profile': profile, **report}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--finesub-src', type=Path, required=True)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--profile', choices=['auto', *compat.PROFILES], default='auto')
    args = parser.parse_args()
    sys.path.insert(0, str(args.finesub_src.resolve()))
    try:
        report = verify_archive(args.archive, args.profile)
        version = args.finesub_src.resolve().parent / 'VERSION'
        report['finesub_version'] = version.read_text(encoding='utf-8').strip() if version.exists() else 'not provided'
        print(json.dumps(report, ensure_ascii=False))
    except Exception as exc:
        raise SystemExit(f'Native FineSub verification failed: {exc}') from exc


if __name__ == '__main__':
    main()
