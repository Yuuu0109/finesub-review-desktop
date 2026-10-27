"""Validate versioned human Markdown projections of FineSub.

The pinned presets come from Yuuu0109/nonoka-sub-x af32b014 and eb642f25.
This adapter does not open a knowledge database or apply sharing verdicts.
"""
import json
import re
import unicodedata
from pathlib import Path

RULES = json.loads(Path(__file__).with_name('node_presets.json').read_text(encoding='utf-8'))
PROFILE = 'finesub-0.5.1'
CURRENT_PROFILE = 'finesub-0.6.0'
CURRENT_RULES = json.loads(Path(__file__).with_name('node_presets_0_6.json').read_text(encoding='utf-8'))
NODE_PROFILES = {
    PROFILE: {'schema': 5, 'rules': RULES, 'patch': 'engine-preserved-values.patch'},
    CURRENT_PROFILE: {'schema': 6, 'rules': CURRENT_RULES, 'patch': 'engine-preserved-values-0.6.0.patch'},
}
CATEGORIES = ('common', 'streamer', 'style')


def rules_for(profile):
    return NODE_PROFILES[profile]['rules']


def profile_for_schema(schema):
    for profile, spec in NODE_PROFILES.items():
        if spec['schema'] == schema:
            return profile
    raise ValueError(f'不支持的原生 schema {schema}；仅支持 schema 5、6，不自动迁移')


def release_profile(profile):
    rules = rules_for(profile)
    return {'schema': 1, 'profile': profile, 'knowledge_schema': NODE_PROFILES[profile]['schema'],
            'engine_reference': rules['repository'] + '@' + rules['commit']}


def validate_release_profile(metadata):
    if not isinstance(metadata, dict) or metadata.get('profile') not in NODE_PROFILES:
        raise ValueError('不支持的发布包知识库版本')
    profile = metadata['profile']
    if metadata != release_profile(profile):
        raise ValueError('发布包的规则、引擎来源或知识库版本不一致')
    return profile


def clean_lines(content):
    """Ignore full-preview guidance; unclosed comments are a format error."""
    lines, opened = [], None
    for number, line in enumerate(content.splitlines(), 1):
        stripped = line.strip()
        if opened is not None:
            if '-->' in stripped:
                opened = None
            continue
        if stripped.startswith('<!--'):
            if not stripped.endswith('-->'):
                opened = number
            continue
        lines.append((number, line))
    if opened is not None:
        raise ValueError(f'第 {opened} 行的 HTML 注释未闭合')
    return lines


def sections(content):
    result, section = [], None
    for number, line in clean_lines(content):
        if line.startswith('## '):
            section = {'name': line[3:].strip(), 'lines': []}
            result.append(section)
        elif section is not None and line.strip():
            body = re.sub(r'^\s*- ', '', line, count=1)
            section['lines'].append((number, body))
    return result


def subject_intro(content):
    for _, line in clean_lines(content)[1:]:
        if line.startswith('#'):
            break
        if line.strip():
            return line.strip()
    return ''


def profile_field(content, name):
    for section in sections(content):
        if section['name'] == '档案':
            for _, body in section['lines']:
                match = re.match(r'^\[([^\[\]]*)\]\s?(.*)$', body)
                if match and match[1].strip() == name:
                    return match[2].strip()
    return None


def native_names(content, title):
    raw = profile_field(content, '本名')
    if raw is None:
        match = re.search(r'^(?:-\s*)?本名[:：]\s*(.*)$', content, re.M)
        raw = match[1] if match else ''
    names = [re.sub(r'[（(][^（）()]*[）)]', '', name).strip() for name in re.split(r'[/／]', raw)]
    return list(dict.fromkeys(name for name in names if name and name != title))


def classify(body, spec):
    label = ''
    match = re.match(r'^\[([^\[\]]*)\]\s?(.*)$', body)
    if match:
        label, body = match[1].strip(), match[2]
        if not label:
            raise ValueError('标记名不能为空')
        if not body.strip():
            return None  # full-preview slot, not a knowledge node
    if 'term' in spec['body_kinds']:
        columns = body.split('|', 3)
        if len(columns) == 4:
            if not columns[0].strip():
                raise ValueError('术语的源语言名称不能为空')
            return {'kind': 'term', 'label': label, 'body': body, 'columns': columns}
        if len(columns) == 3:
            raise ValueError('术语行必须四列：源语言|中文定名|别名|一句话描述；无别名时第三列留空')
    if 'note' not in spec['body_kinds']:
        raise ValueError('本节只接受四列术语行')
    return {'kind': 'note', 'label': label, 'body': body}


def normalize(value):
    # Preset section identity removes spaces but preserves case.
    return ''.join(unicodedata.normalize('NFKC', value).split())


def match_key(value):
    # Native key/label matching keeps internal spaces and uses casefold.
    return unicodedata.normalize('NFKC', value.strip()).casefold()


def validate(content, category, profile=PROFILE):
    errors, warnings = [], []
    if len(content.encode('utf-8')) > 1_000_000:
        errors.append('FineSub 当前条目内容不能超过 1,000,000 UTF-8 字节')
    try:
        lines = clean_lines(content)
        if not lines or not lines[0][1].startswith('# '):
            errors.append('FineSub 当前格式要求首行是一级标题')
        intro = []
        for _, line in lines[1:]:
            if line.startswith('## '):
                break
            if line.strip():
                intro.append(line)
        if len(intro) > 1:
            errors.append('一级标题与首个章节之间只能有一行简介')
        parsed = sections(content)
    except ValueError as exc:
        return [], [str(exc)]
    preset = rules_for(profile)['categories'][category]
    declared = {normalize(s['name']): s for s in preset['sections']}
    seen, section_labels = set(), {}
    for section in parsed:
        name = section['name']
        normalized = normalize(name)
        if normalized in seen:
            errors.append(f'章节重复：{name}')
        seen.add(normalized)
        if name == '元数据':
            allowed = {'最近更新日期'}
            if profile == CURRENT_PROFILE:
                allowed.add('重分类理由')
            for number, body in section['lines']:
                field = re.match(r'^([^:：]+)[:：]', body)
                if not field or field[1].strip() not in allowed:
                    errors.append(f'「元数据」第 {number} 行：不支持的元数据字段')
            continue
        spec = declared.get(normalized) or preset.get('default_section')
        if spec is None:
            errors.append(f'{category} 不接受章节「{name}」；请归入规定章节或待归类')
            continue
        labels, count = set(), 0
        for number, body in section['lines']:
            try:
                node = classify(body, spec)
                if node is None:
                    continue
                count += 1
                label = node['label']
                if label and match_key(label) in labels:
                    raise ValueError(f'本节标记 [{label}] 重复')
                if label:
                    labels.add(match_key(label))
                if spec.get('max_body_chars') and len(body) > spec['max_body_chars']:
                    raise ValueError(f'每行最多 {spec["max_body_chars"]} 字（含标记）')
            except ValueError as exc:
                errors.append(f'「{name}」第 {number} 行：{exc}')
        section_labels[normalized] = labels
        if spec.get('max_lines') and count > spec['max_lines']:
            errors.append(f'「{name}」最多 {spec["max_lines"]} 行，实际 {count} 行')
        if spec.get('staging') and count:
            warnings.append(f'「{name}」有 {count} 行待归类内容，请核实归位；不会自动共享')
    for section in parsed:
        name = section['name']
        spec = declared.get(normalize(name)) or preset.get('default_section')
        if spec and spec.get('labels_from'):
            known = section_labels.get(normalize(spec['labels_from']), set())
            for label in section_labels.get(normalize(name), set()) - known:
                errors.append(f'「{name}」的 [{label}] 在「{spec["labels_from"]}」没有对应约定')
    return warnings, errors
