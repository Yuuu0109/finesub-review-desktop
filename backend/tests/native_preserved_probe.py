"""Native semantic checks occur only on the caller's isolated copied store."""
import argparse
import json
import sys
from dataclasses import asdict
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--engine-src', type=Path, required=True)
parser.add_argument('--knowledge-root', type=Path, required=True)
parser.add_argument('--archive', type=Path, required=True)
parser.add_argument('--report', type=Path, required=True)
args = parser.parse_args()
sys.path.insert(0, str(args.engine_src))
sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'scripts'))
from preserved_values import apply_release, fingerprint, plan_subject, read_archive
from finesub.llm.knowledge.node.repo import KnowledgeRepo
from finesub.llm.knowledge.node.envelope import Binding, Envelope
from finesub.llm.knowledge.node.apply import apply_envelope, preview
from finesub.llm.knowledge.node.model import payload_group_hash
from finesub.llm.knowledge.node.presets import preset_for_category
from finesub.llm.knowledge.node.signals import record_evidence
from finesub.llm.knowledge.verify import unverified_claims, _placements, _externally_verifiable

root = args.knowledge_root.resolve()
assert root.is_relative_to(args.report.parent.resolve()), 'Probe must use its own temporary directory'
repo = KnowledgeRepo.open(root, auto_import=False)

def state():
    nodes, items, memberships, policies = {}, {}, {}, {}
    placements = _placements(repo.store, repo.rev)
    for subject in repo.store.subjects():
        nodes[subject.local_id] = asdict(subject)
        items[subject.local_id] = [asdict(item) for item in repo.store.items_of(subject.local_id)]
        preset = preset_for_category(subject.payload['category'])
        for member in repo.store.children(subject.local_id):
            node = repo.store.node(member.child_id)
            nodes[node.local_id] = asdict(node)
            items[node.local_id] = [asdict(item) for item in repo.store.items_of(node.local_id)]
            memberships[member.membership_id] = asdict(member)
            policies[node.local_id] = (preset.share_for(member.section, node.payload.get('label')), preset.verify_for(member.section, node.payload.get('label')), node.visibility,
                                       node.kind in ('note', 'term') and _externally_verifiable(node, placements))
    return nodes, items, memberships, policies

try:
    rows = read_archive(args.archive)
    tasks = [(next(s for s in repo.subjects(name.split('/')[0]) if s.payload['surface'] == Path(name).stem), baseline, target) for name, baseline, target in rows]
    before = state()
    counts_before = [sum(claim['node_id'] in {m.child_id for m in repo.store.children(subject.local_id)} for claim in unverified_claims(repo.store, limit=10000)) for subject, _, _ in tasks]
    result = apply_release(repo, tasks, write=True)
    assert len(result['changed_nodes']) == 3
    after = state()
    assert set(before[0]) == set(after[0])
    assert all(before[0][key]['kind'] == after[0][key]['kind'] for key in before[0])
    assert before[1:] == after[1:], 'Matching, memberships, or policies changed'
    subject_ids = {s.local_id for s, _, _ in tasks}
    for key in before[0]:
        if key not in set(result['changed_nodes']) | subject_ids:
            assert before[0][key] == after[0][key], 'Unrelated node changed'
        else:
            old, new = dict(before[0][key]), dict(after[0][key])
            for field in ('valid_from_rev', 'canonical_id', 'payload'):
                old.pop(field); new.pop(field)
            assert old == new
    counts_after = [sum(claim['node_id'] in {m.child_id for m in repo.store.children(subject.local_id)} for claim in unverified_claims(repo.store, limit=10000)) for subject, _, _ in tasks]
    for subject, baseline, target in tasks:
        assert fingerprint(repo.entry_text(subject.local_id)) == fingerprint(target)
        assert all(s['name'] != '待归类' or not s['lines'] for s in __import__('app.node_compat', fromlist=['projection']).projection(repo.entry_text(subject.local_id))['sections'])
        old_prompt = repo.entry_injection_text(subject.local_id, rev=2)
        new_prompt = repo.entry_injection_text(subject.local_id)
        assert '副本兼容验证' in new_prompt and old_prompt != new_prompt
    # Old claim evidence remains, and editing its value creates a new claim hash.
    changed = next(repo.store.node(key) for key in result['changed_nodes'] if repo.store.node(key).kind == 'note')
    unchanged = next(repo.store.node(m.child_id) for m in repo.store.children(tasks[0][0].local_id) if m.child_id not in result['changed_nodes'] and repo.store.node(m.child_id).kind == 'note')
    for node in (changed, unchanged):
        record_evidence(repo.store, node_id=node.local_id, field_path='payload:core', value_hash=payload_group_hash(node.kind, node.payload), verdict='confirmed', evidence_kind='external', task_id='isolated-compat-evidence', source_ref='https://example.invalid/isolated-test')
    evidence_before = [dict(row) for row in repo.store.conn.execute('SELECT * FROM evidence ORDER BY dedupe_key')]
    old_hash = payload_group_hash(changed.kind, changed.payload)
    field = 'text'
    envelope = Envelope(task_id='evidence-check', assignment_id='probe', context_epoch=0, knowledge_read_rev=repo.rev,
                        ops=[{'op':'update_preserved_value','id':'@node','value':changed.payload[field]+'（再次验证）'}],
                        handle_bindings=[Binding('@node','node',changed.local_id,changed.valid_from_rev)])
    edited = apply_envelope(repo.store,envelope,revision_kind='user',atomic=True)
    assert not edited.rolled_back and not edited.conflicts and not edited.rejected_ops
    assert old_hash != payload_group_hash(changed.kind,repo.store.node(changed.local_id).payload)
    evidence_after = [dict(row) for row in repo.store.conn.execute('SELECT * FROM evidence ORDER BY dedupe_key')]
    assert all(row in evidence_after for row in evidence_before), 'Existing evidence was lost or overwritten'
    assert any(c['node_id']==changed.local_id for c in unverified_claims(repo.store,limit=10000))
    assert not any(c['node_id']==unchanged.local_id for c in unverified_claims(repo.store,limit=10000))
    # A successful intro edit also leaves every old body node untouched.
    intro_subject = repo.store.node(tasks[2][0].local_id)
    intro_baseline = repo.entry_text(intro_subject.local_id)
    intro_target = intro_baseline.replace(intro_subject.payload['intro'], intro_subject.payload['intro']+'（原生简介验证）', 1)
    child_before = [(asdict(m), asdict(repo.store.node(m.child_id))) for m in repo.store.children(intro_subject.local_id)]
    intro_result = apply_release(repo, [(intro_subject,intro_baseline,intro_target)],write=True)
    assert intro_result['operations']==1 and not intro_result['changed_nodes']
    assert child_before == [(asdict(m), asdict(repo.store.node(m.child_id))) for m in repo.store.children(intro_subject.local_id)]
    # A stale node plus an otherwise-current intro edit must roll back as a unit.
    subject = repo.store.node(tasks[0][0].local_id)
    old_intro = subject.payload['intro']
    stale = Envelope(task_id='stale-check', assignment_id='probe', context_epoch=0, knowledge_read_rev=envelope.knowledge_read_rev,
                     ops=[{**envelope.ops[0], 'value':changed.payload[field]+'（不同的过期编辑）'}]+[{'op':'update','id':'@subject','set':{'payload.intro':'不能写入的冲突简介'}}],
                     handle_bindings=envelope.handle_bindings+[Binding('@subject','node',subject.local_id,subject.valid_from_rev)])
    rejected = apply_envelope(repo.store,stale,revision_kind='user',atomic=True)
    assert rejected.rolled_back and repo.store.node(subject.local_id).payload['intro']==old_intro
    # Legacy creation and identity changes remain forbidden.
    fact = next(node for node in repo.store.nodes_of_kind('fact'))
    forbidden = Envelope(task_id='identity-check', assignment_id='probe', context_epoch=0, knowledge_read_rev=repo.rev,
                         ops=[{'op':'update_preserved_value','id':'@fact','value':'只改值'}, {'op':'update','id':'@fact','set':{'payload.field':'新字段'}}],
                         handle_bindings=[Binding('@fact','node',fact.local_id,fact.valid_from_rev)])
    invalid = apply_envelope(repo.store,forbidden,revision_kind='user',atomic=True)
    assert invalid.rolled_back or invalid.rejected_ops
    assert repo.store.node(fact.local_id).payload == fact.payload
    result_report = {'counts':list(map(list,zip(counts_before,counts_after))), 'changed_nodes':result['changed_nodes'],
                     'original_node_ids_and_kinds_preserved':True, 'memberships_and_matching_items_unchanged':True,
                     'unrelated_payloads_unchanged':True, 'sharing_and_verification_policies_unchanged':True,
                     'prompt_projection_includes_edited_value':True, 'evidence_history_preserved':True,
                     'native_user_provenance_evidence_added':len(evidence_after)-len(evidence_before),
                     'intro_edit_keeps_old_children':True,
                     'modified_value_gets_new_hash_and_requalifies':True, 'atomic_conflict_preserved_other_fields':True,
                     'forbidden_identity_change_rejected':True, 'real_store_written':False}
    args.report.write_text(json.dumps(result_report,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps(result_report,ensure_ascii=False))
finally:
    KnowledgeRepo.forget(root)
