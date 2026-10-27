"""Side-effect-free FineSub Markdown adapter; never executes imported content."""
import hashlib
import io
import json
import re
import stat
import unicodedata
import zipfile
from datetime import date
from pathlib import PurePosixPath

from . import node_markdown, node_compat

MAX_FILE = 2 * 1024 * 1024
MAX_TOTAL = 30 * 1024 * 1024
MAX_FILES = 2000
PROFILES = {
    'legacy-preserve': {'name': '现有 Markdown · 保留分类', 'types': None},
    'finesub-code-v17': {'name': 'FineSub 本地 v17 校验规则', 'types': ['游戏', '梗', '事件', '人物']},
    node_markdown.CURRENT_PROFILE: {'name': 'FineSub 0.6.0 · schema 6', 'types': None},
    node_markdown.PROFILE: {'name': 'FineSub 0.5.1 · schema 5', 'types': None},
}


def digest(value):
    return hashlib.sha256(value.encode('utf-8') if isinstance(value, str) else value).hexdigest()


def safe_path(path):
    value = unicodedata.normalize('NFC', path.replace('\\', '/'))
    parts = PurePosixPath(value).parts
    if not parts or value.startswith('/') or any(p in ('..', '.') or ':' in p or any(ord(c) < 32 for c in p) for p in parts):
        raise ValueError(f'不安全的文件路径：{path}')
    if len(value) > 450 or any(re.search(r'[<>:"|?*]', p) or p.endswith((' ', '.')) for p in parts):
        raise ValueError(f'文件路径不兼容 Windows：{path}')
    if any(re.fullmatch(r'(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?', p, re.I) for p in parts):
        raise ValueError(f'文件名是 Windows 保留名称：{path}')
    return '/'.join(parts)


def knowledge_path(path):
    parts = safe_path(path).split('/')
    for i, part in enumerate(parts):
        if part in (*node_markdown.CATEGORIES, 'translation'):
            return '/'.join(parts[i:])
    raise ValueError('请保留 streamer、common、style 或 translation 目录；单文件请在上传时选择所属分类')


def unpack(filename, data):
    safe_path(filename)
    if len(data) > MAX_TOTAL:
        raise ValueError('上传内容超过 30 MB')
    if filename.lower().endswith('.zip'):
        try:
            with zipfile.ZipFile(io.BytesIO(data)) as z:
                members = z.infolist()
                if len(members) > MAX_FILES or sum(x.file_size for x in members) > MAX_TOTAL:
                    raise ValueError('压缩包超过 2000 个文件或解压后超过 30 MB')
                result = []
                seen = set()
                for item in members:
                    path = safe_path(item.filename)
                    if stat.S_ISLNK(item.external_attr >> 16):
                        raise ValueError('压缩包不能包含符号链接')
                    if item.is_dir() or any(p.startswith('.') or p == '__MACOSX' for p in path.split('/')):
                        continue
                    if path.casefold() in seen:
                        raise ValueError(f'压缩包存在重名文件：{path}')
                    seen.add(path.casefold())
                    if item.file_size > MAX_FILE:
                        raise ValueError(f'单个文件超过 2 MB：{path}')
                    if not path.lower().endswith(('.md', '.jsonl')):
                        result.append((path, None))
                        continue
                    result.append((path, z.read(item)))
                return result
        except (zipfile.BadZipFile, RuntimeError) as exc:
            raise ValueError(f'无法解压 ZIP：{exc}') from exc
    if len(data) > MAX_FILE:
        raise ValueError('单个文件超过 2 MB')
    return [(filename, data)]


def text_decode(data):
    try:
        value = data.decode('utf-8-sig')
    except UnicodeDecodeError as exc:
        raise ValueError('文件不是 UTF-8，请转换编码后重新上传') from exc
    if '\x00' in value:
        raise ValueError('文件包含二进制内容')
    return value


def select_sources(files):
    """A current rendered projection supersedes the frozen Markdown archive."""
    rendered_roots = set()
    for path, data in files:
        parts = safe_path(path).split('/')
        if data is not None and path.lower().endswith('.md') and 'rendered' in parts:
            offset = parts.index('rendered')
            if offset + 1 < len(parts) and parts[offset + 1] in node_markdown.CATEGORIES:
                rendered_roots.add(tuple(parts[:offset]))
    for path, _ in files:
        parts = safe_path(path).split('/')
        if parts[-1] == 'knowledge.sqlite' and tuple(parts[:-1]) not in rendered_roots:
            raise ValueError('knowledge.sqlite 是当前知识源；请先导出当前 rendered/ 文本，不能用历史 Markdown 目录代替当前数据库')
    selected, notices = [], []
    for path, data in files:
        parts = safe_path(path).split('/')
        category_at = next((i for i, part in enumerate(parts) if part in node_markdown.CATEGORIES), None)
        if category_at is not None and 'rendered' not in parts and tuple(parts[:category_at]) in rendered_roots and path.lower().endswith('.md'):
            notices.append(f'已使用当前 rendered/ 投影，跳过历史档案：{path}')
            continue
        selected.append((path, data))
    return selected, notices


def index_types(files):
    result = {}
    for filename, data in files:
        if data is None or not filename.lower().endswith('.md'):
            continue
        try:
            path = knowledge_path(filename)
        except ValueError:
            continue
        if path != 'common/index.md':
            continue
        try:
            text = text_decode(data)
        except ValueError:
            continue  # worker reports the invalid index; it cannot supply metadata
        for line in text.splitlines():
            match = re.match(r'^-\s+(.+?)\s+\[([^\]]+)\]\s*\|', line)
            if match:
                result[f'common/{match[1].strip()}.md'] = match[2].strip()
    return result


def is_node_markdown(content):
    return bool(re.search(r'^(?:-\s+)?\[[^\[\]]+\]', content, re.M))


def field(content, name):
    m = re.search(r'^\s*(?:-\s*)?' + re.escape(name) + r'[:：]\s*(.*)$', content, re.M)
    return m.group(1).strip() if m else ''


def metadata(path, content):
    try:
        visible = '\n'.join(line for _, line in node_markdown.clean_lines(content))
    except ValueError:
        visible = content
    heading = re.search(r'^#{1,3}\s+(.+?)\s*$', visible, re.M)
    title = heading.group(1) if heading else PurePosixPath(path).stem
    lines = visible.splitlines()
    intro_lines = []
    for line in lines[1:]:
        if line.startswith('#'):
            break
        if line.strip():
            intro_lines.append(line.strip())
    intro = intro_lines[0] if intro_lines else ''
    category = path.split('/')[0]
    if '/mistakes/' in path:
        title = field(content, '原文片段').strip('`') or title
        intro = field(content, '正确译文').strip('`')
    if path == 'translation/featured.md':
        title, intro = '翻译纠错精选', '控制进入纠错提示的精选条目'
    def value(name):
        try:
            current = node_markdown.profile_field(content, name)
        except ValueError:
            current = None
        return current if current is not None else field(content, name)
    return {'title': title[:200], 'category': category, 'kind': value('类型'), 'aliases': value('别名'), 'summary': intro[:350]}


def validate(path, content, profile='legacy-preserve', baseline=None):
    warnings, errors = [], []
    safe_path(path)
    if not content.strip():
        errors.append('内容为空')
    if profile in node_markdown.NODE_PROFILES and path.startswith('translation/'):
        errors.append('FineSub 0.5.1 / 0.6.0 不再读取旧 translation 纠错文件；保留旧归档请使用 legacy-preserve，当前知识请人工整理到对应条目或 style')
        return warnings, errors
    if path.startswith(tuple(category + '/' for category in node_markdown.CATEGORIES)):
        if len(path.split('/')) != 2 or not path.endswith('.md'):
            errors.append('词条必须是分类目录下的 Markdown 文件')
        meta = metadata(path, content)
        if not re.search(r'^#\s+', content, re.M):
            errors.append('缺少一级标题')
        elif meta['title'] != PurePosixPath(path).stem:
            errors.append('一级标题与文件名不一致')
        if profile in node_markdown.NODE_PROFILES or path.startswith('style/'):
            node_profile = profile if profile in node_markdown.NODE_PROFILES else node_markdown.PROFILE
            w, e = node_markdown.validate(content, path.split('/')[0], node_profile)
            if not errors and profile in node_markdown.NODE_PROFILES and baseline is not None and path.startswith(('common/', 'streamer/')):
                _, old_errors = node_markdown.validate(baseline, path.split('/')[0], profile)
                if old_errors or node_compat.is_legacy_projection(baseline):
                    return node_compat.validate(baseline, content, path.split('/')[0], profile)
            return warnings + w, errors + e
        for section in ('档案', '元数据'):
            if not re.search(r'^##\s+' + section + r'\s*$', content, re.M):
                warnings.append(f'缺少「{section}」章节')
        if not re.search(r'来源|#\d+|https?://|\d{1,2}:\d{2}', content):
            warnings.append('正文未发现明确来源，建议补充节目期数或证据')
        allowed = PROFILES[profile]['types']
        if path.startswith('common/') and allowed and meta['kind'] not in allowed:
            errors.append(f'当前目标格式只接受分类：{"、".join(allowed)}；实际为「{meta["kind"] or "未填写"}」')
    elif '/mistakes/' in path:
        for name in ('原文片段', '错误译文', '正确译文', '说明'):
            if not field(content, name):
                errors.append(f'纠错记录缺少「{name}」')
        if not re.fullmatch(r'M\d{4,}', PurePosixPath(path).stem):
            errors.append('纠错编号必须是 M 加至少四位数字')
        if not re.search(r'^###\s+' + re.escape(PurePosixPath(path).stem) + r'\s*$', content, re.M):
            errors.append('纠错标题编号与文件编号不一致')
    elif path == 'translation/featured.md':
        ids = re.findall(r'^-\s+(M\d{4,})\b', content, re.M)
        if len(ids) > 30 or len(ids) != len(set(ids)):
            errors.append('精选最多 30 条且编号不能重复')
        if any(line.strip() and not line.startswith('#') and not re.match(r'^-\s+M\d{4,}\b', line) for line in content.splitlines()):
            errors.append('精选正文必须每行使用「- M0001」格式')
    elif '/examples/' in path:
        if not field(content, '原文片段') or not field(content, '精修译文'):
            errors.append('示例缺少原文片段或精修译文')
        if not re.search(r'^###\s+' + re.escape(PurePosixPath(path).stem) + r'\s*$', content, re.M):
            errors.append('示例标题编号与文件编号不一致')
    else:
        errors.append('不支持的知识文件路径')
    return warnings, errors


def split_markdown(path, content):
    if path in tuple(category + '/index.md' for category in node_markdown.CATEGORIES):
        return [], ['索引作为参考保留，发布时根据获准正文重新生成']
    if path in ('translation/common-mistake.md', 'translation/good-example.md'):
        is_mistake = path.endswith('common-mistake.md')
        prefix = 'M' if is_mistake else 'G'
        matches = list(re.finditer(r'^###\s+(' + prefix + r'\d{4,})\s*$', content, re.M))
        if len({m.group(1) for m in matches}) != len(matches):
            raise ValueError('翻译文件包含重复编号')
        result = []
        for i, match in enumerate(matches):
            body = content[match.start():matches[i + 1].start() if i + 1 < len(matches) else len(content)].strip() + '\n'
            result.append((f'translation/{"mistakes" if is_mistake else "examples"}/{match.group(1)}.md', body))
        if is_mistake:
            featured = re.search(r'^##\s+精选\s*\n(.*?)(?=^##\s|\Z)', content, re.M | re.S)
            result.append(('translation/featured.md', '# 翻译纠错精选\n\n' + (featured.group(1).strip() + '\n' if featured else '')))
        if not matches and content.strip() and not is_mistake:
            raise ValueError('未找到有效的翻译示例编号')
        return result, []
    return [(path, content)], []


def index_line(path, content, kind_hint=''):
    meta = metadata(path, content)
    clean = lambda x: x.replace('|', '／').replace('\n', ' ').strip()
    kind = meta['kind'] or kind_hint
    key = meta['title'] + (f' [{kind}]' if path.startswith('common/') and kind else '')
    names = '、'.join(node_markdown.native_names(content, meta['title']))
    return f'- {clean(key)} | {clean(names)} | {clean(meta["aliases"])} | {clean(node_markdown.subject_intro(content))}'


def build_files(documents, profile, kind_hints=None, baselines=None):
    files, warnings, errors = {}, [], []
    if profile in node_markdown.NODE_PROFILES:
        names = {}
        for path in documents:
            if path.startswith(('common/', 'streamer/')):
                key = node_markdown.match_key(PurePosixPath(path).stem)
                if key in names:
                    errors.append(f'FineSub 可匹配条目名称跨分类重复：{names[key]} / {path}')
                names[key] = path
    for path, content in documents.items():
        w, e = validate(path, content, profile, baseline=(baselines or {}).get(path))
        warnings.extend(f'{path}：{x}' for x in w)
        errors.extend(f'{path}：{x}' for x in e)
        if path.startswith(tuple(category + '/' for category in node_markdown.CATEGORIES)):
            files[path] = content
    # Native style subjects are resolved explicitly and have no matching index.
    for category in ('common', 'streamer'):
        lines = [index_line(path, content, (kind_hints or {}).get(path, '')) for path, content in sorted(files.items()) if path.startswith(category + '/')]
        files[f'{category}/index.md'] = '\n'.join(lines) + ('\n' if lines else '')
    mistakes = {PurePosixPath(p).stem: c for p, c in documents.items() if p.startswith('translation/mistakes/')}
    featured = documents.get('translation/featured.md', '')
    featured_lines = [x for x in featured.splitlines() if x.strip().startswith('- ')]
    ids = re.findall(r'^-\s+(M\d{4,})\b', featured, re.M)
    for mid in ids:
        if mid not in mistakes:
            errors.append(f'精选引用未发布的纠错记录：{mid}')
    if mistakes or featured:
        files['translation/common-mistake.md'] = '# 常见翻译错误\n\n条目 id 永不复用；精选最多 30 条，由人工维护。\n\n## 精选\n\n' + '\n'.join(featured_lines) + '\n\n## 条目\n\n' + '\n'.join(mistakes[k].strip() + '\n' for k in sorted(mistakes))
    examples = [c for p, c in sorted(documents.items()) if p.startswith('translation/examples/')]
    if examples:
        files['translation/good-example.md'] = '# 优质翻译示例\n\n' + '\n'.join(examples)
    compatible = sorted(p for p, c in documents.items() if node_compat.NOTICE in validate(p, c, profile, baseline=(baselines or {}).get(p))[0])
    if compatible and not errors:
        manifest = {**node_markdown.release_profile(profile), 'schema': 2,
                    'mode': 'preserve-existing-values', 'entries': [
                        {'path': path, 'baseline': baselines[path], 'baseline_sha256': digest(baselines[path]),
                         'target_sha256': digest(documents[path])} for path in compatible]}
        files['finesub-review-compat.json'] = json.dumps(manifest, ensure_ascii=False, indent=2)
    if profile in node_markdown.NODE_PROFILES and not errors:
        files['finesub-review-profile.json'] = json.dumps(node_markdown.release_profile(profile), ensure_ascii=False, indent=2)
    return files, {'warnings': warnings, 'errors': errors, 'files': len(files), 'profile': profile, 'verification': 'adapter-validation',
                   'compatibility_entries': compatible, 'native_import_requires_compat_tool': bool(compatible)}


def make_zip(files):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for path, content in sorted(files.items()):
            archive.writestr(path, content.encode('utf-8') if isinstance(content, str) else content)
    return output.getvalue()


def _knowledge_proposals(text, current, profile='legacy-preserve'):
    """Render a JSONL change group against immutable current snapshots.

    edit_lines requires base_sha256. A group is all-or-nothing at publication.
    """
    match = re.search(r'<knowledge_proposals[^>]*>(.*?)</knowledge_proposals>', text, re.S)
    if match:
        text = match.group(1)
    text = re.sub(r'^```(?:jsonl|json)?\s*\n?|\n?```\s*$', '', text.strip())
    docs = {p: c for p, c in current.items()}
    changed, origins, deleted = set(), {}, set()
    for line_no, line in enumerate(text.splitlines(), 1):
        if not line.strip():
            continue
        try:
            p = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f'JSONL 第 {line_no} 行无效') from exc
        if not isinstance(p, dict):
            raise ValueError(f'JSONL 第 {line_no} 行必须是对象')
        category, entry, op = p.get('category'), p.get('entry'), p.get('op')
        if category not in ('common', 'streamer') or not isinstance(entry, str) or '/' in entry or '\\' in entry:
            raise ValueError('提案需要有效的 category 和 entry')
        path = safe_path(f'{category}/{entry}.md')
        if op == 'create_entry':
            if path in docs:
                raise ValueError(f'新建目标已经存在：{path}')
            if not p.get('intro'):
                raise ValueError('新建词条需要 intro')
            if len(str(p.get('reason', '')).strip()) < 10:
                raise ValueError('新建词条需要 reason，说明未并入已有词条的原因（至少 10 字）')
            if profile in node_markdown.NODE_PROFILES:
                docs[path] = f'# {entry}\n{p["intro"]}\n\n## 档案\n- [本名] {entry}\n- [别名] {"、".join(p.get("aliases", []))}\n' + (f'- [类型] {p.get("entry_type", "")}\n' if category == 'common' and p.get('entry_type') else '') + '\n## 元数据\n'
            else:
                docs[path] = f'# {entry}\n{p["intro"]}\n\n## 档案\n本名: {entry}\n别名: {",".join(p.get("aliases", []))}\n' + (f'类型: {p.get("entry_type", "")}\n' if category == 'common' else '') + '\n## 元数据\n'
        else:
            if path not in docs:
                raise ValueError(f'找不到基础词条：{path}')
            if p.get('base_sha256') and p['base_sha256'] != digest(current.get(path, '')):
                raise ValueError(f'基础快照哈希不匹配：{path}')
            if op == 'delete_entry':
                deleted.add(path)
            elif op == 'rename_entry':
                new_key = p.get('new_key', '')
                if not new_key or '/' in new_key or '\\' in new_key:
                    raise ValueError('重命名需要有效 new_key')
                target = safe_path(f'{category}/{new_key}.md')
                if target in docs:
                    raise ValueError('重命名目标已经存在')
                docs[target] = re.sub(r'^#\s+.*$', '# ' + new_key, docs.pop(path), count=1, flags=re.M)
                origins[target] = origins.pop(path, path)
                changed.discard(path)
                path = target
            elif op in ('append_lines', 'replace_section'):
                section, body = p.get('section', ''), p.get('content', '')
                if not section or section == '元数据' or not body.strip():
                    raise ValueError('章节提案需要非空 section/content，且不能改写元数据')
                pattern = r'^##\s+' + re.escape(section) + r'\s*\n(.*?)(?=^##\s|\Z)'
                found = re.search(pattern, docs[path], re.S | re.M)
                if found:
                    old = found.group(1).strip()
                    additions = [x for x in body.splitlines() if x.strip() not in {y.strip() for y in old.splitlines()}]
                    replacement = body.strip() if op == 'replace_section' else old + ('\n' if additions else '') + '\n'.join(additions)
                    docs[path] = docs[path][:found.start()] + f'## {section}\n{replacement}\n\n' + docs[path][found.end():]
                else:
                    offset = docs[path].find('## 元数据')
                    offset = offset if offset >= 0 else len(docs[path])
                    docs[path] = docs[path][:offset] + f'## {section}\n{body.strip()}\n\n' + docs[path][offset:]
            elif op == 'edit_lines':
                if not p.get('base_sha256'):
                    raise ValueError('按行修改必须提供 base_sha256；请附加对应基础快照后重试')
                if path in changed:
                    raise ValueError('按行修改不能与同一文件的其他修改混用，请先合并成一个 edit_lines 提案')
                lines = docs[path].splitlines()
                edits = p.get('edits', [])
                if not edits or len({e.get('line') for e in edits}) != len(edits):
                    raise ValueError('行修改为空或位置重复')
                for edit in sorted(edits, key=lambda e: e.get('line', 0), reverse=True):
                    n, action = edit.get('line', 0), edit.get('action')
                    if not isinstance(n, int) or n < 1 or n > len(lines):
                        raise ValueError('修改行号超出基础快照范围')
                    if action == 'remove':
                        del lines[n - 1]
                    elif action == 'change':
                        lines[n - 1:n] = str(edit.get('content', '')).splitlines()
                    elif action == 'insert_after':
                        lines[n:n] = str(edit.get('content', '')).splitlines()
                    else:
                        raise ValueError('未知行修改操作')
                docs[path] = '\n'.join(lines) + '\n'
            else:
                raise ValueError(f'不支持的提案操作：{op}')
        changed.add(path)
    return [{'path': p, 'origin_path': origins.get(p, p), 'content': docs[p], 'operation': 'delete' if p in deleted else 'upsert', 'grouped': True} for p in sorted(changed)]


def proposals(text, current, reserved_paths=None, profile='legacy-preserve'):
    blocks = re.findall(r'<(knowledge_proposals|mistake_proposals)\b[^>]*>(.*?)</\1>', text, re.S)
    text_blocks = [body for _, body in blocks] if blocks else [re.sub(r'^```(?:jsonl|json)?\s*\n?|\n?```\s*$', '', text.strip())]
    items = []
    for block in text_blocks:
        for line_no, line in enumerate(block.splitlines(), 1):
            if not line.strip():
                continue
            try:
                value = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f'JSONL 第 {line_no} 行无效') from exc
            if not isinstance(value, dict):
                raise ValueError('JSONL 每行必须是一个对象')
            items.append(value)
    knowledge = [p for p in items if p.get('op') not in ('add_mistake', 'set_featured', 'add_example')]
    results = _knowledge_proposals('\n'.join(json.dumps(p, ensure_ascii=False) for p in knowledge), current, profile) if knowledge else []
    docs = dict(current)
    reserved = set(current) | set(reserved_paths or [])
    changed = set()
    for proposal in items:
        operation = proposal.get('op')
        if operation == 'set_featured':
            ids = proposal.get('ids', [])
            if not isinstance(ids, list) or len(ids) > 30 or len(set(ids)) != len(ids):
                raise ValueError('精选编号必须是不重复的列表，最多 30 条')
            if any(f'translation/mistakes/{mid}.md' not in docs for mid in ids):
                raise ValueError('精选引用了不存在的纠错编号')
            path = 'translation/featured.md'
            docs[path] = '# 翻译纠错精选\n\n' + '\n'.join(f'- {mid}' for mid in ids) + '\n'
            changed.add(path)
        elif operation in ('add_mistake', 'add_example'):
            mistake = operation == 'add_mistake'
            fields = [('source', '原文片段'), ('wrong', '错误译文'), ('correct', '正确译文'), ('note', '说明')] if mistake else [('source', '原文片段'), ('translation', '精修译文'), ('note', '说明')]
            if any(not isinstance(proposal.get(key), str) or not proposal[key].strip() for key, _ in fields):
                raise ValueError('翻译提案缺少必需字段：' + '、'.join(key for key, _ in fields))
            if any('\n' in proposal[key] or '\r' in proposal[key] for key, _ in fields):
                raise ValueError('翻译提案各字段必须为单行文本')
            folder, prefix = ('mistakes', 'M') if mistake else ('examples', 'G')
            comparable = lambda content: tuple(field(content, label).strip('`') for _, label in fields)
            key = tuple(proposal[key].strip().strip('`') for key, _ in fields)
            if any(comparable(content) == key for path, content in docs.items() if path.startswith(f'translation/{folder}/')):
                continue
            numbers = [int(PurePosixPath(path).stem[1:]) for path in reserved if re.fullmatch(r'translation/' + folder + '/' + prefix + r'\d{4,}\.md', path)]
            entry_id = f'{prefix}{max(numbers, default=0) + 1:04d}'
            path = f'translation/{folder}/{entry_id}.md'
            reserved.add(path)
            docs[path] = f'### {entry_id}\n' + '\n'.join(f'- {label}: `{proposal[key].strip()}`' if label != '说明' else f'- {label}: {proposal[key].strip()}' for key, label in fields) + f'\n- 记录时间: {date.today().strftime("%Y-%m")}\n'
            changed.add(path)
    results.extend({'path': path, 'origin_path': path, 'content': docs[path], 'operation': 'upsert', 'grouped': True} for path in sorted(changed))
    if not items:
        raise ValueError('提案文件没有包含有效 JSONL 操作')
    return results
