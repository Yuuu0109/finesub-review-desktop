"""Explicit existing-knowledge compatibility import, dry-run by default."""
import argparse
import json
import shutil
import sqlite3
import sys
import tempfile
from contextlib import closing, contextmanager
from pathlib import Path

from preserved_values import apply_release, check_engine, engine_profile, read_archive
from app import node_markdown


@contextmanager
def working_root(root, *, write):
    if write:
        yield root
        return
    # Even opening the native store runs DDL/migrations. A dry-run must not
    # create a revision, run that initializer, or refresh files on the source.
    with tempfile.TemporaryDirectory(prefix='finesub-compat-preview-') as tmp:
        copied = Path(tmp)
        with closing(sqlite3.connect((root / 'knowledge.sqlite').as_uri() + '?mode=ro', uri=True)) as source:
            with closing(sqlite3.connect(copied / 'knowledge.sqlite')) as destination:
                source.backup(destination)
        if (root / 'rendered').exists():
            shutil.copytree(root / 'rendered', copied / 'rendered')
        yield copied


def run(root, rows, *, write):
    from finesub.llm.knowledge.node.repo import KnowledgeRepo
    with working_root(root, write=write) as active_root:
        repo = KnowledgeRepo.open(active_root, auto_import=False)
        try:
            if repo.rendered_dirty_files():
                raise ValueError('rendered 存在未收录修改，请先保留并人工合并；兼容工具不会收割或覆盖这些文件，未写入')
            tasks = []
            for name, baseline, target in rows:
                category = name.split('/')[0]
                subjects = [s for s in repo.subjects(category) if s.payload['surface'] == Path(name).stem]
                if len(subjects) != 1:
                    raise ValueError('兼容主体不存在或不唯一：' + name)
                tasks.append((subjects[0], baseline, target))
            result = apply_release(repo, tasks, write=write)
            if write:
                repo.refresh_rendered()
            return result
        finally:
            KnowledgeRepo.forget(active_root)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--finesub-src', type=Path, required=True)
    parser.add_argument('--knowledge-root', type=Path, required=True)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--apply', action='store_true', help='Explicitly write after backup and successful dry-run')
    args = parser.parse_args()
    sys.path.insert(0, str(args.finesub_src.resolve()))
    check_engine()  # Refuse unsupported engines before opening a writable store.
    profile = engine_profile()
    expected_schema = node_markdown.NODE_PROFILES[profile]['schema']
    rows = read_archive(args.archive, expected_profile=profile)
    root = args.knowledge_root.resolve()
    if not (root / 'knowledge.sqlite').is_file():
        raise ValueError('兼容工具只接受已有原生知识库，不能凭 Markdown 重建旧节点类型')
    with closing(sqlite3.connect((root / 'knowledge.sqlite').as_uri() + '?mode=ro', uri=True)) as check:
        schema = check.execute("SELECT value FROM meta WHERE key='schema_version'").fetchone()
    if not schema or schema[0] != str(expected_schema):
        raise ValueError(f'引擎 {profile} 要求 schema {expected_schema}，实际知识库为 {schema[0] if schema else "未知"}；不会自动迁移知识库')
    result = run(root, rows, write=args.apply)
    print(json.dumps({'dry_run': not args.apply, 'profile': profile, 'schema': expected_schema, 'entries': len(rows), 'result': result,
                      'scope': 'Only manifest compatibility entries; other current-format files are not applied here.'}, ensure_ascii=False))

if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        raise SystemExit(str(exc)) from exc
