import argparse
import getpass
import os
import secrets
import time
from pathlib import Path

from sqlalchemy import delete, select

from . import compat
from .db import AccessGate, AccessSession, Asset, Batch, DATA, Job, Session, User, init_db
from .security import hash_password
from .services import confirm_batch
from .worker import run_once


def wait_for_job(job_id, timeout=600):
    """Wait until a claimed job reaches a terminal state; None if it never does."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        with Session() as db:
            job = db.get(Job, job_id)
            status = job.status if job else None
        if status in ('done', 'failed'):
            return status
        time.sleep(1)
    return None


def main():
    parser = argparse.ArgumentParser(description='FineSub 审核站管理命令')
    sub = parser.add_subparsers(dest='command', required=True)
    create = sub.add_parser('init-admin')
    create.add_argument('--username', default='admin')
    create.add_argument('--name', default='管理员')
    create.add_argument('--generate', action='store_true', help='生成随机密码并写入本地 data/initial-account.txt')
    gate = sub.add_parser('set-passphrase', help='设置或更换访问口令，并使已有访问会话失效')
    gate.add_argument('--generate', action='store_true', help='生成随机口令，保存至 data/access-passphrase.txt')
    imp = sub.add_parser('import-directory')
    imp.add_argument('directory')
    imp.add_argument('--username', default='admin')
    imp.add_argument('--name', default='现有知识库 · 首次导入')
    args = parser.parse_args()
    init_db()
    if args.command == 'init-admin':
        with Session.begin() as db:
            if db.scalar(select(User.id).where(User.role == 'admin')):
                raise SystemExit('已有账号，请使用管理员页面管理用户')
            password = secrets.token_urlsafe(18) if args.generate else getpass.getpass('管理员密码（至少 10 位）: ')
            if len(password) < 10:
                raise SystemExit('管理员密码至少 10 位')
            db.add(User(username=args.username.lower(), name=args.name, password=hash_password(password), role='admin'))
            if args.generate:
                DATA.mkdir(parents=True, exist_ok=True)
                credentials = DATA / 'initial-account.txt'
                credentials.write_text(f'账号: {args.username.lower()}\n密码: {password}\n登录后可在账号设置中修改密码。请妥善保管此文件。\n', encoding='utf-8')
                print(f'初始账号已写入 {credentials}')
        print('管理员已创建')
    elif args.command == 'set-passphrase':
        passphrase = secrets.token_urlsafe(18) if args.generate else getpass.getpass('访问口令（至少 10 位）: ')
        if len(passphrase) < 10:
            raise SystemExit('访问口令至少 10 位')
        hashed = hash_password(passphrase)
        with Session.begin() as db:
            gate = db.get(AccessGate, 1)
            if gate:
                gate.passphrase_hash = hashed
            else:
                db.add(AccessGate(id=1, passphrase_hash=hashed))
            db.execute(delete(AccessSession))
        if args.generate:
            credentials = DATA / 'access-passphrase.txt'
            credentials.write_text(passphrase + '\n', encoding='utf-8')
            print(f'访问口令已写入 {credentials}')
        print('访问口令已设置，已有访问会话已失效；审核记录保留')
    elif args.command == 'import-directory':
        root = Path(args.directory).resolve(strict=True)
        files = {p.relative_to(root).as_posix(): p.read_bytes() for p in root.rglob('*.md') if '.git' not in p.parts}
        data = compat.make_zip(files)
        with Session.begin() as db:
            user = db.scalar(select(User).where(User.username == args.username, User.role == 'admin', User.active.is_(True)))
            if not user:
                raise SystemExit('找不到有效管理员')
            batch = Batch(name=args.name, source=f'只读导入：{root.name}', author_id=user.id)
            db.add(batch)
            db.flush()
            batch_id, user_id = batch.id, user.id
            db.add(Asset(batch_id=batch.id, filename='knowledge.zip', data=data, digest=compat.digest(data)))
            job = Job(kind='import', target_id=batch.id)
            db.add(job)
            db.flush()
            job_id = job.id
        # Run only this import; a queued release from another process must not
        # be consumed by the CLI. run_once returns False when another worker won
        # the claim, which is not a failure: wait for that worker's result.
        run_once(job_id)
        if wait_for_job(job_id) is None:
            raise SystemExit('导入任务由其他后台进程接管，仍未结束；请稍后查看任务状态')
        from .db import ImportItem
        with Session.begin() as db:
            batch = db.get(Batch, batch_id)
            if batch.status != 'preview':
                raise SystemExit('导入失败，请查看后台任务日志')
            items = db.scalars(select(ImportItem).where(ImportItem.batch_id == batch_id)).all()
            errors = [i.error for i in items if i.error]
            if errors:
                raise SystemExit('导入预检存在错误：' + '；'.join(errors))
            ids = [i.id for i in items if i.classification != 'duplicate']
            if ids:
                result = confirm_batch(db, batch_id, ids, db.get(User, user_id))
                print(f'已导入 {result["created"]} 个待审核项，未自动裁决或发布')
            else:
                print('全部为重复内容，没有新增待审项')


if __name__ == '__main__':
    main()
