"""Review navigation and publication readiness use the same version boundaries."""
from sqlalchemy import select

from app.db import Revision, Session
from app.worker import run_once
from test_workflow import approve, client, document, isolated_database, latest, publish, upload


def test_personal_review_filter_ignores_replies_and_other_members_and_resets_on_revision():
    a, m, other = client(), client('member'), client('member')
    upload(a)
    entry = latest(a)
    path = f'/api/entries/{entry["entry_id"]}'
    record = other.post(path + '/reviews', json={'revision_id': entry['id'], 'verdict': 'approve', 'body': '已核对'}).json()
    m.post(path + '/reviews', json={'revision_id': entry['id'], 'parent_id': record['id'], 'verdict': 'reply', 'body': '回复不算审核'})
    assert m.get('/api/entries?reviewed=no').json()['total'] == 1
    assert other.get('/api/entries?reviewed=no').json()['total'] == 0
    m.post(path + '/reviews', json={'revision_id': entry['id'], 'verdict': 'changes', 'body': '补充出处'})
    assert m.get('/api/entries?reviewed=yes').json()['items'][0]['my_review']
    assert m.get('/api/entries?reviewed=no').json()['total'] == 0
    revised = a.post(path + '/revisions', json={'base_revision': entry['id'], 'content': document(extra='- 新出处。')})
    assert revised.status_code == 200
    assert m.get('/api/entries?reviewed=no').json()['total'] == 1
    assert not m.get('/api/entries').json()['items'][0]['my_review']


def test_cursor_does_not_skip_after_decision_and_preserves_scope():
    a = client()
    batch = upload(a, {f'common/词条{i}.md': document(f'词条{i}') for i in range(4)})
    upload(a, {'common/其他批次.md': document('其他批次')})
    # Equal timestamps must still yield a deterministic, complete traversal.
    with Session.begin() as db:
        for rev in db.scalars(select(Revision)).all():
            rev.created_at = '2026-10-22T00:00:00+00:00'
    url = f'/api/entries?status=pending&category=common&batch_id={batch["id"]}'
    ordered = a.get(url).json()['items']
    assert len(ordered) == 4
    for index, item in enumerate(ordered):
        approve(a, item)
        result = a.get(url + f'&after={item["id"]}&limit=1').json()
        assert [r['id'] for r in result['items']] == ([ordered[index + 1]['id']] if index < 3 else [])
    assert a.get(url + f'&after={ordered[0]["id"]}&page=2').status_code == 422
    assert a.get('/api/entries?after=missing').status_code == 404


def test_diff_can_compare_to_published_instead_of_previous_revision():
    a = client()
    upload(a)
    first = latest(a)
    approve(a, first)
    publish(a, [first])
    path = f'/api/entries/{first["entry_id"]}'
    second = a.post(path + '/revisions', json={'base_revision': first['id'], 'content': document(extra='- 第二版新增依据。')}).json()
    third = a.post(path + '/revisions', json={'base_revision': second['id'], 'content': document(extra='- 第二版新增依据。\n- 第三版补充。')}).json()
    previous = a.get(path).json()
    formal = a.get(path + '?compare_to=published').json()
    assert previous['comparison']['id'] == second['id']
    assert formal['comparison']['id'] == first['id']
    assert '+- 第二版新增依据。' not in previous['diff']
    assert '+- 第二版新增依据。' in formal['diff']
    assert formal['entry']['id'] == third['id']
    assert a.get(path + '?compare_to=unknown').status_code == 422


def test_release_candidates_explain_group_blockers_without_publishing():
    a, m = client(), client('member')
    upload(a, {'common/甲.md': document('甲'), 'common/乙.md': document('乙')})
    items = a.get('/api/entries').json()['items']
    with Session.begin() as db:
        for rev in db.scalars(select(Revision)).all():
            rev.group_id = 'linked-proposal'
    approve(a, items[0])
    blocked = a.get('/api/releases/candidates').json()
    assert len(blocked) == 1 and any('尚未通过' in e for e in blocked[0]['blockers'])
    assert blocked[0]['related'][0]['id'] == items[1]['id']
    assert len(a.get('/api/entries?group_id=linked-proposal').json()['items']) == 2
    assert a.get('/api/entries/' + items[0]['entry_id']).json()['related'][0]['id'] == items[1]['id']
    assert m.get('/api/releases/candidates').status_code == 403
    assert a.get('/api/releases').json() == []
    approve(a, items[1])
    ready = a.get('/api/releases/candidates').json()
    assert len(ready) == 2 and not any(r['blockers'] for r in ready)
    # Readiness never bypasses the existing atomic group check at publication.
    assert a.post('/api/releases', json={'name': '不完整', 'revision_ids': [items[0]['id']]}).status_code == 409
    publish(a, items)
    assert a.get('/api/releases/candidates').json() == []


def test_published_group_deletion_does_not_block_later_survivor_revision():
    a = client()
    upload(a, {'common/甲.md': document('甲'), 'common/乙.md': document('乙')})
    items = a.get('/api/entries').json()['items']
    with Session.begin() as db:
        for rev in db.scalars(select(Revision)).all():
            rev.group_id = 'linked-proposal'
    for item in items:
        approve(a, item)
    publish(a, items)
    deleted = a.post('/api/entries/' + items[1]['entry_id'] + '/revisions', json={
        'base_revision': items[1]['id'], 'content': '', 'operation': 'delete'}).json()
    approve(a, deleted)
    publish(a, [deleted])
    survivor = a.post('/api/entries/' + items[0]['entry_id'] + '/revisions', json={
        'base_revision': items[0]['id'], 'content': document(items[0]['title'], extra='- 新的依据。')}).json()
    approve(a, survivor)
    candidates = a.get('/api/releases/candidates').json()
    assert len(candidates) == 1 and candidates[0]['blockers'] == []
    assert candidates[0]['related'] == []
    release_id = publish(a, [survivor])
    release = next(r for r in a.get('/api/releases').json() if r['id'] == release_id)
    assert release['status'] == 'ready' and release['count'] == 1


def test_worker_rechecks_group_if_already_published_peer_changes_while_queued():
    a = client()
    upload(a, {'common/甲.md': document('甲'), 'common/乙.md': document('乙')})
    items = a.get('/api/entries').json()['items']
    with Session.begin() as db:
        for rev in db.scalars(select(Revision)).all():
            rev.group_id = 'linked-proposal'
    for item in items:
        approve(a, item)
    publish(a, items)
    first = a.post('/api/entries/' + items[0]['entry_id'] + '/revisions', json={
        'base_revision': items[0]['id'], 'content': document(items[0]['title'], extra='- 新的依据。')}).json()
    approve(a, first)
    queued = publish(a, [first], run=False)
    response = a.post('/api/entries/' + items[1]['entry_id'] + '/revisions', json={
        'base_revision': items[1]['id'], 'content': document(items[1]['title'], extra='- 待审核的关联变更。')})
    assert response.status_code == 200
    assert run_once()
    release = next(r for r in a.get('/api/releases').json() if r['id'] == queued)
    assert release['status'] == 'failed'
    assert '关联变更组' in release['report']['errors'][0]
    assert not release['current']
