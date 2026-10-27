"""Conservative value edits against an existing immutable Markdown baseline.

This is admission for review, not proof of native write compatibility. Legacy
node kinds require the separately supplied engine operation on reapplication.
"""
import re

from . import node_markdown as grammar

NOTICE = '历史正文定点兼容：保留原章节、节点行体及匹配字段；支持简介和现有值的修订，兼容知识库包需用专用导入工具应用'
IDENTITY_FIELDS = {'本名', '别名', '类型', '读音', '読み', '読み方'}


def projection(content):
    """Keep raw node rows; whitespace and bullets can be stored user content."""
    visible = grammar.clean_lines(content)
    if not visible or not visible[0][1].startswith('# '):
        raise ValueError('兼容正文首行必须是一级标题')
    intro = []
    sections = []
    current = None
    for _, raw in visible[1:]:
        if raw.startswith('## '):
            current = {'name': raw[3:].strip(), 'lines': []}
            sections.append(current)
        elif raw.strip():
            if current is None:
                intro.append(raw)
            else:
                current['lines'].append(raw)
    if len(intro) > 1:
        raise ValueError('兼容正文只支持一行简介')
    if len({grammar.normalize(s['name']) for s in sections}) != len(sections):
        raise ValueError('兼容正文不能含重复章节')
    return {'title': visible[0][1], 'intro': intro[0] if intro else '', 'sections': sections}


def is_legacy_projection(content):
    """Old identity rows can parse today while still owning legacy fact nodes."""
    try:
        parsed = projection(content)
    except ValueError:
        return False
    for section in parsed['sections']:
        if section['name'] != '档案':
            continue
        for raw in section['lines']:
            field = re.match(r'^\s*(?:- )?([^\[\]:：]+)[:：]', raw)
            if field and field[1].strip() in IDENTITY_FIELDS:
                return True
    return False


def value_edit(before, after):
    """Allow only a stable label/colon identity or a term description edit."""
    if '误听' in before or '误听' in after:
        raise ValueError('含误听匹配提示的旧行需要专门核对索引，本兼容路径不修改该行')
    old = re.match(r'^(\s*(?:- )?)(.*)$', before)
    new = re.match(r'^(\s*(?:- )?)(.*)$', after)
    if old[1] != new[1] or not new[2].strip():
        raise ValueError('不能改动列表缩进或清空旧节点')
    old_body, new_body = old[2], new[2]
    old_label = re.match(r'^\[([^\[\]]+)\]\s?', old_body)
    new_label = re.match(r'^\[([^\[\]]+)\]\s?', new_body)
    if bool(old_label) != bool(new_label) or old_label and old_label[0] != new_label[0]:
        raise ValueError('不能改动旧节点标记')
    if old_label:
        if old_label[1] in IDENTITY_FIELDS:
            raise ValueError('匹配身份字段须通过当前格式的专门迁移修改')
        old_body, new_body = old_body[old_label.end():], new_body[new_label.end():]
    if '|' in old_body or '|' in new_body:
        old_columns, new_columns = old_body.split('|', 3), new_body.split('|', 3)
        if len(old_columns) != 4 or len(new_columns) != 4 or old_columns[:3] != new_columns[:3]:
            raise ValueError('兼容术语只允许修改第四列描述，不能改变源名、中文名或别名')
    else:
        field = re.match(r'^(.+?[:：]\s*)(.*)$', old_body)
        if field:
            if field[1].rstrip(' :：') in IDENTITY_FIELDS:
                raise ValueError('不能通过兼容编辑改变匹配身份字段')
            if not new_body.startswith(field[1]) or not new_body[len(field[1]):].strip():
                raise ValueError('只能修改旧字段的值，字段名、日期或关系目标须保留')
        elif re.match(r'^\[', new_body) or re.match(r'^#{1,2}\s', new_body):
            raise ValueError('不能把旧散文变成新标记或章节')


def preserving_changes(baseline, content, category, profile=grammar.PROFILE):
    before, after = projection(baseline), projection(content)
    if before['title'] != after['title']:
        raise ValueError('兼容保存不能重命名主体')
    if [s['name'] for s in before['sections']] != [s['name'] for s in after['sections']]:
        raise ValueError('兼容保存不能新增、删除、移动或重命名旧章节')
    if len(content.encode('utf-8')) > 1_000_000:
        raise ValueError('FineSub 当前条目内容不能超过 1,000,000 UTF-8 字节')
    changes = []
    preset = grammar.rules_for(profile)['categories'][category]
    declared = {grammar.normalize(s['name']): s for s in preset['sections']}
    for old, new in zip(before['sections'], after['sections']):
        if len(old['lines']) != len(new['lines']):
            raise ValueError(f'「{old["name"]}」兼容保存不能新增或删除节点行')
        for position, (old_raw, new_raw) in enumerate(zip(old['lines'], new['lines'])):
            if old_raw == new_raw:
                continue
            if old['name'] == '元数据':
                raise ValueError('兼容保存的元数据由原生工具维护，不能在正文编辑中改写')
            value_edit(old_raw, new_raw)
            spec = declared.get(grammar.normalize(old['name'])) or preset.get('default_section')
            if spec and spec.get('max_body_chars') and len(new_raw) > max(len(old_raw), spec['max_body_chars'] + 2):
                raise ValueError(f'「{old["name"]}」不能增加超限旧行的长度')
            changes.append({'section': old['name'], 'position': position, 'before': old_raw, 'after': new_raw})
    return before, after, changes


def validate(baseline, content, category, profile=grammar.PROFILE):
    try:
        preserving_changes(baseline, content, category, profile)
    except ValueError as exc:
        return [], [f'旧知识无法可靠兼容保存：{exc}。原内容和草稿仍保留']
    return [NOTICE], []
