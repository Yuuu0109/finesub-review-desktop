"""Native schema 6 behavior, confined to a fresh caller-owned test store."""
import argparse
import json
import sys
import uuid
from dataclasses import asdict
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--engine-src', type=Path, required=True)
parser.add_argument('--root', type=Path, required=True)
parser.add_argument('--report', type=Path, required=True)
args = parser.parse_args()
assert not args.root.exists() and args.root.resolve().is_relative_to(args.report.parent.resolve())
sys.path[:0] = [str(args.engine_src), str(Path(__file__).resolve().parents[2] / 'scripts')]
from preserved_values import apply_release, check_engine, fingerprint
from finesub.llm.knowledge.node.apply import apply_envelope
from finesub.llm.knowledge.node.edit import edit_subject
from finesub.llm.knowledge.node.envelope import Binding, Envelope
from finesub.llm.knowledge.node.human_marks import ack_lines, line_marks
from finesub.llm.knowledge.node.repo import KnowledgeRepo

check_engine('finesub-0.6.0')
repo = KnowledgeRepo.open(args.root, auto_import=False)


def model_edit(node_id, value):
    node = repo.store.node(node_id)
    envelope = Envelope(task_id='isolated-human-marks', assignment_id='probe', context_epoch=0,
        knowledge_read_rev=repo.rev, ops=[{'op': 'update', 'id': '@line', 'set': {'payload.text': value}}],
        handle_bindings=[Binding('@line', 'node', node_id, node.valid_from_rev)])
    result = apply_envelope(repo.store, envelope, revision_kind='harness', atomic=True)
    assert not result.rolled_back and not result.rejected_ops and not result.conflicts


try:
    subject_id = str(uuid.uuid4())
    with repo.store.begin('user') as tx:
        subject = tx.create_node(subject_id, 'subject', {'surface': '人工认可验收', 'category': 'common', 'intro': ''})
    text = '# 人工认可验收\n测试简介\n\n## 档案\n- [本名] 人工认可验收\n- [别名] 可见别名\n\n## 背景\n- [正文] 人工原文\n- [旁注] 人工附注\n'
    edited = edit_subject(repo, subject, text, refresh=False)
    assert not edited.rejected and not edited.staged
    children = {repo.store.node(m.child_id).payload.get('label'): m.child_id for m in repo.store.children(subject_id)}
    main, side = children['正文'], children['旁注']
    with repo.store.begin('import') as tx:
        tx.create_item(str(uuid.uuid4()), subject_id, 'aliases', '待确认别名', maturity='tentative')
    model_edit(main, '模型第一版')
    model_edit(side, '模型附注')
    assert set(line_marks(repo.store, subject_id).live) == {main, side}
    ack_lines(repo.store, subject_id, main)
    assert main not in line_marks(repo.store, subject_id).live
    model_edit(main, '模型第二版')
    marks = line_marks(repo.store, subject_id)
    assert marks.live[main].acked and not marks.live[side].acked
    assert '人认可版' in repo.entry_text(subject_id) and '人改版' in repo.entry_text(subject_id)
    assert '人认可版' not in repo.entry_injection_text(subject_id)
    acks = [dict(row) for row in repo.store.conn.execute('SELECT * FROM line_acks ORDER BY ack_id')]
    aliases = [asdict(i) for i in repo.store.items_of(subject_id)]
    side_mark = asdict(marks.live[side])
    baseline = repo.entry_text(subject_id)
    target = baseline.replace('- [正文] 模型第二版', '- [正文] 审核通过正文')
    result = apply_release(repo, [(repo.store.node(subject_id), baseline, target)], write=True)
    assert result['changed_nodes'] == [main]
    assert acks == [dict(row) for row in repo.store.conn.execute('SELECT * FROM line_acks ORDER BY ack_id')]
    assert aliases == [asdict(i) for i in repo.store.items_of(subject_id)]
    marks = line_marks(repo.store, subject_id)
    assert main not in marks.live and asdict(marks.live[side]) == side_mark
    assert fingerprint(repo.entry_text(subject_id)) == fingerprint(target)
    # Metadata and comment-only changes neither endorse the head nor other lines.
    current = repo.entry_text(subject_id)
    rev = repo.rev
    noop = apply_release(repo, [(repo.store.node(subject_id), current, current + '\n<!-- 人工认可注释仍由原生引擎维护 -->\n')], write=True)
    assert noop['operations'] == 0 and repo.rev == rev
    model_edit(main, '模型第三版')
    newest = line_marks(repo.store, subject_id).live[main]
    assert not newest.acked and '审核通过正文' in newest.value
    assert asdict(line_marks(repo.store, subject_id).live[side]) == side_mark
    assert repo.store.conn.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    report = {'historical_acks_preserved': True, 'untouched_human_marks_preserved': True,
              'reviewed_value_is_new_human_endorsement': True, 'next_model_change_shows_reviewed_value': True,
              'tentative_and_visible_aliases_preserved': True, 'comment_only_change_has_no_revision': True,
              'prompt_excludes_human_history': True, 'database_integrity': True}
    args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False))
finally:
    KnowledgeRepo.forget(args.root)
