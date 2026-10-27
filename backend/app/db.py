"""Shared SQLAlchemy storage. SQLite locally; PostgreSQL in deployment."""
import contextlib
import json
import logging
import os
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path

from sqlalchemy import JSON, Boolean, ForeignKey, Integer, LargeBinary, String, Text, create_engine, event, inspect, text
from sqlalchemy.exc import IntegrityError, OperationalError, ProgrammingError
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, sessionmaker


log = logging.getLogger('finesub-review.db')


def uid():
    return uuid.uuid4().hex


def now():
    return datetime.now(timezone.utc).isoformat()


ROOT = Path(__file__).resolve().parents[2]
DATA = Path(os.getenv('REVIEW_DATA_DIR', str(ROOT / 'data')))
DATABASE_URL = os.getenv('DATABASE_URL', f'sqlite:///{DATA / "review.db"}')
if DATABASE_URL.startswith('sqlite'):
    DATA.mkdir(parents=True, exist_ok=True)
engine = create_engine(DATABASE_URL, connect_args={'check_same_thread': False, 'timeout': 30} if DATABASE_URL.startswith('sqlite') else {}, pool_pre_ping=True)
if DATABASE_URL.startswith('sqlite'):
    @event.listens_for(engine, 'connect')
    def sqlite_settings(connection, _):
        connection.execute('PRAGMA foreign_keys=ON')
        connection.execute('PRAGMA journal_mode=WAL')

Session = sessionmaker(engine, expire_on_commit=False)


class Base(DeclarativeBase):
    pass


class User(Base):
    __tablename__ = 'users'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    username: Mapped[str] = mapped_column(String(80), unique=True)
    name: Mapped[str] = mapped_column(String(80))
    password: Mapped[str] = mapped_column(Text)
    role: Mapped[str] = mapped_column(String(16), default='member')
    active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class LoginSession(Base):
    __tablename__ = 'login_sessions'
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(ForeignKey('users.id'), index=True)
    csrf: Mapped[str] = mapped_column(String(64))
    expires: Mapped[int] = mapped_column(Integer)


class AccessGate(Base):
    __tablename__ = 'access_gate'
    id: Mapped[int] = mapped_column(Integer, primary_key=True, default=1)
    passphrase_hash: Mapped[str] = mapped_column(Text)


class AccessSession(Base):
    __tablename__ = 'access_sessions'
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(ForeignKey('users.id'), index=True)
    csrf: Mapped[str] = mapped_column(String(64))
    expires: Mapped[int] = mapped_column(Integer)


class Workspace(Base):
    __tablename__ = 'workspace'
    id: Mapped[int] = mapped_column(Integer, primary_key=True, default=1)
    name: Mapped[str] = mapped_column(String(100), default='FineSub 知识库')
    serial: Mapped[int] = mapped_column(Integer, default=0)
    profile: Mapped[str] = mapped_column(String(40), default='legacy-preserve')
    latest_release: Mapped[str | None] = mapped_column(String(32), nullable=True)


class Entry(Base):
    __tablename__ = 'entries'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    path: Mapped[str] = mapped_column(String(500), unique=True)
    current_revision: Mapped[str | None] = mapped_column(String(32), nullable=True)
    published_revision: Mapped[str | None] = mapped_column(String(32), nullable=True)
    assignee_id: Mapped[str | None] = mapped_column(ForeignKey('users.id'), nullable=True, index=True)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Revision(Base):
    __tablename__ = 'revisions'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    entry_id: Mapped[str] = mapped_column(ForeignKey('entries.id'), index=True)
    parent_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    base_published: Mapped[str | None] = mapped_column(String(32), nullable=True)
    batch_id: Mapped[str | None] = mapped_column(String(32), nullable=True, index=True)
    number: Mapped[int] = mapped_column(Integer)
    path: Mapped[str] = mapped_column(String(500))
    group_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    content: Mapped[str] = mapped_column(Text)
    digest: Mapped[str] = mapped_column(String(64))
    title: Mapped[str] = mapped_column(String(200))
    category: Mapped[str] = mapped_column(String(40))
    kind: Mapped[str] = mapped_column(String(80), default='')
    aliases: Mapped[str] = mapped_column(Text, default='')
    summary: Mapped[str] = mapped_column(Text, default='')
    source: Mapped[str] = mapped_column(Text, default='')
    operation: Mapped[str] = mapped_column(String(20), default='upsert')
    status: Mapped[str] = mapped_column(String(24), default='pending', index=True)
    author_id: Mapped[str] = mapped_column(ForeignKey('users.id'))
    created_at: Mapped[str] = mapped_column(String(40), default=now, index=True)


class Batch(Base):
    __tablename__ = 'batches'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    name: Mapped[str] = mapped_column(String(160))
    source: Mapped[str] = mapped_column(Text, default='')
    status: Mapped[str] = mapped_column(String(24), default='queued')
    author_id: Mapped[str] = mapped_column(ForeignKey('users.id'))
    warnings: Mapped[list] = mapped_column(JSON, default=list)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Asset(Base):
    __tablename__ = 'assets'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    batch_id: Mapped[str] = mapped_column(ForeignKey('batches.id'), index=True)
    filename: Mapped[str] = mapped_column(String(500))
    digest: Mapped[str] = mapped_column(String(64))
    # Uploaded bytes are only needed for import and download, never for listings.
    data: Mapped[bytes] = mapped_column(LargeBinary, deferred=True)


class ImportItem(Base):
    __tablename__ = 'import_items'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    batch_id: Mapped[str] = mapped_column(ForeignKey('batches.id'), index=True)
    path: Mapped[str] = mapped_column(String(500))
    origin_path: Mapped[str] = mapped_column(String(500), default='')
    grouped: Mapped[bool] = mapped_column(Boolean, default=False)
    content: Mapped[str] = mapped_column(Text)
    base_revision: Mapped[str | None] = mapped_column(String(32), nullable=True)
    operation: Mapped[str] = mapped_column(String(20), default='upsert')
    classification: Mapped[str] = mapped_column(String(24))
    warnings: Mapped[list] = mapped_column(JSON, default=list)
    error: Mapped[str] = mapped_column(Text, default='')


class Review(Base):
    __tablename__ = 'reviews'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    revision_id: Mapped[str] = mapped_column(ForeignKey('revisions.id'), index=True)
    author_id: Mapped[str] = mapped_column(ForeignKey('users.id'))
    parent_id: Mapped[str | None] = mapped_column(ForeignKey('reviews.id'), nullable=True)
    verdict: Mapped[str] = mapped_column(String(24))
    body: Mapped[str] = mapped_column(Text)
    quote: Mapped[str] = mapped_column(Text, default='')
    suggestion: Mapped[str] = mapped_column(Text, default='')
    evidence: Mapped[str] = mapped_column(Text, default='')
    resolution: Mapped[str] = mapped_column(String(24), default='open')
    resolution_note: Mapped[str] = mapped_column(Text, default='')
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Decision(Base):
    __tablename__ = 'decisions'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    revision_id: Mapped[str] = mapped_column(ForeignKey('revisions.id'), index=True)
    author_id: Mapped[str] = mapped_column(ForeignKey('users.id'))
    status: Mapped[str] = mapped_column(String(24))
    reason: Mapped[str] = mapped_column(Text)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Release(Base):
    __tablename__ = 'releases'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    name: Mapped[str] = mapped_column(String(120))
    notes: Mapped[str] = mapped_column(Text, default='')
    author_id: Mapped[str] = mapped_column(ForeignKey('users.id'))
    parent_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    rollback_of: Mapped[str | None] = mapped_column(String(32), nullable=True)
    profile: Mapped[str] = mapped_column(String(40))
    snapshot: Mapped[dict] = mapped_column(JSON, default=dict)
    changes: Mapped[list] = mapped_column(JSON, default=list)
    status: Mapped[str] = mapped_column(String(24), default='queued')
    report: Mapped[dict] = mapped_column(JSON, default=dict)
    # Release listings only need metadata; ZIPs load on download or in the worker.
    artifact: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True, deferred=True)
    archive: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True, deferred=True)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Job(Base):
    __tablename__ = 'jobs'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    kind: Mapped[str] = mapped_column(String(24))
    target_id: Mapped[str] = mapped_column(String(32), index=True)
    status: Mapped[str] = mapped_column(String(24), default='queued', index=True)
    error: Mapped[str] = mapped_column(Text, default='')
    attempts: Mapped[int] = mapped_column(Integer, default=0)
    touched_at: Mapped[str] = mapped_column(String(40), default=now)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Notice(Base):
    __tablename__ = 'notices'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    user_id: Mapped[str] = mapped_column(ForeignKey('users.id'), index=True)
    entry_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    text: Mapped[str] = mapped_column(Text)
    read: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


class Audit(Base):
    __tablename__ = 'audit'
    id: Mapped[str] = mapped_column(String(32), primary_key=True, default=uid)
    actor_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    action: Mapped[str] = mapped_column(String(64))
    target_id: Mapped[str] = mapped_column(String(80), default='')
    detail: Mapped[dict] = mapped_column(JSON, default=dict)
    created_at: Mapped[str] = mapped_column(String(40), default=now)


def _default_literal(column):
    """Literal for a NOT NULL column that carries only a Python-side default."""
    if column.server_default is not None:
        argument = column.server_default.arg
        return argument.text if hasattr(argument, 'text') else str(argument)
    default = column.default
    if default is None:
        return None
    if not getattr(default, 'is_scalar', False):
        # `default=list` / `default=dict` mean "start empty"; both are safe to
        # express as a literal, and existing rows should start empty too.
        # SQLAlchemy wraps the callable, so compare by name, not identity.
        name = getattr(default.arg, '__name__', '')
        if name == 'list':
            return "'[]'"
        if name == 'dict':
            return "'{}'"
        return None
    value = default.arg
    if isinstance(value, bool):
        if engine.dialect.name == 'postgresql':
            return 'TRUE' if value else 'FALSE'
        return '1' if value else '0'
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, (list, dict)):
        value = json.dumps(value)
    if not isinstance(value, str):
        return None
    return "'" + value.replace("'", "''") + "'"


def _add_column_ddl(table, column):
    """ALTER TABLE for a column the models have and the database lacks.

    A nullable column always works. A NOT NULL column needs a literal default,
    because existing rows have no value; when none can be derived safely, refuse
    and say why instead of guessing. Returns (ddl, reason): exactly one is None.
    """
    try:
        compiled = column.type.compile(dialect=engine.dialect)
    except Exception as exc:  # noqa: BLE001 - report the real cause, do not swallow it
        return None, f'column type {column.type!r} could not be compiled ({exc})'
    if column.nullable or column.primary_key:
        return f'ALTER TABLE {table.name} ADD COLUMN {column.name} {compiled}', None
    literal = _default_literal(column)
    if literal is None:
        return None, ('NOT NULL and its Python-side default cannot be expressed as a literal '
                      '(needs a server_default or a scalar/empty-container default)')
    return f'ALTER TABLE {table.name} ADD COLUMN {column.name} {compiled} NOT NULL DEFAULT {literal}', None


def _migrate_tolerantly(connection, ddl, label):
    """Apply one migration statement; a concurrent winner or old engine is not fatal."""
    try:
        with connection.begin_nested():
            connection.execute(text(ddl))
        log.info('schema migration applied: %s', label)
        return True
    except (IntegrityError, OperationalError, ProgrammingError) as exc:
        log.warning('schema migration skipped: %s (%s)', label, exc)
        return False


def _lock_path():
    """Lock file beside the database it protects.

    Derived from the engine URL, not REVIEW_DATA_DIR: the two are independent
    settings, and a database pointed elsewhere with a shared DATA folder would
    mean two processes locking different files and never serializing at all.
    """
    database = engine.url.database
    if not database or database == ':memory:':
        return DATA / '.schema-init.lock'
    return Path(database).resolve().parent / '.schema-init.lock'


@contextlib.contextmanager
def _schema_lock(timeout=60):
    """Serialize schema startup across processes on SQLite.

    Retrying create_all against SQLite is not enough: writers serialize on the
    database file, so two processes retrying at once keep colliding until
    SQLite's per-lock timeout expires, and 3 retries x 30s turns a fast failure
    into a stall. An OS-level lock makes the loser wait outside the database
    instead, then see a complete schema and do nothing. PostgreSQL needs no
    equivalent here; its DDL is transactional and the retry below suffices.
    """
    if engine.dialect.name != 'sqlite':
        yield
        return
    path = _lock_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = open(path, 'a+b')
    acquired = False
    try:
        deadline = time.monotonic() + timeout
        if os.name == 'nt':
            import msvcrt
            while True:
                try:
                    msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
                    acquired = True
                    break
                except OSError:
                    # LK_NBLCK fails immediately, so an unrelated OS error (a
                    # bad descriptor, say) must not become a busy loop.
                    if time.monotonic() >= deadline:
                        log.warning('schema lock not acquired within %ss: %s', timeout, path)
                        raise
                    time.sleep(0.1)
        else:
            import fcntl
            while True:
                try:
                    fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    acquired = True
                    break
                except OSError:
                    if time.monotonic() >= deadline:
                        log.warning('schema lock not acquired within %ss: %s', timeout, path)
                        raise
                    time.sleep(0.1)
        try:
            yield
        finally:
            if acquired:
                if os.name == 'nt':
                    import msvcrt
                    handle.seek(0)
                    msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
                else:
                    import fcntl
                    fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
    finally:
        handle.close()


def _create_missing_tables(attempts=3):
    for attempt in range(1, attempts + 1):
        try:
            Base.metadata.create_all(engine)
            return
        except (IntegrityError, OperationalError, ProgrammingError) as exc:
            # Several processes can create the same table at once and the loser
            # rolls back. On SQLite _schema_lock already serializes this, so the
            # retry covers PostgreSQL and callers that bypass the lock.
            # PostgreSQL reports DuplicateTable as ProgrammingError, not
            # IntegrityError.
            if attempt == attempts:
                raise
            log.warning('table creation raced (%s), retrying: %s', attempt, exc)
            time.sleep(0.2 * attempt)


class SchemaOutOfDate(RuntimeError):
    """The database is missing columns that cannot be added automatically."""


def _sync_schema(connection):
    """Bring an existing database up to the models without redefining tables.

    Built from the model metadata rather than a hand-written list: a whitelist
    has silently missed columns twice (`entries.assignee_id`,
    `revisions.group_id`), and `create_all` never adds columns to tables that
    already exist. A column that cannot be added safely is collected and raised
    at the end: starting with a missing column only defers the failure to the
    first query, where it becomes an unexplained 500.
    """
    inspector = inspect(connection)
    existing = set(inspector.get_table_names())
    refused = []
    for table in Base.metadata.sorted_tables:
        if table.name not in existing:
            continue
        actual = {column['name'] for column in inspector.get_columns(table.name)}
        for column in table.columns:
            if column.name in actual:
                continue
            ddl, reason = _add_column_ddl(table, column)
            if ddl is None:
                refused.append(f'{table.name}.{column.name}: {reason}')
                continue
            if _migrate_tolerantly(connection, ddl, f'add column {table.name}.{column.name}'):
                continue
            # A concurrent process may have added the same column first; that is
            # success, not a refused migration. Re-inspect before deciding.
            if column.name in {c['name'] for c in inspect(connection).get_columns(table.name)}:
                log.info('column %s.%s was added by another process', table.name, column.name)
                continue
            refused.append(f'{table.name}.{column.name}: ALTER was rejected by the database')
    inspector = inspect(connection)
    for table in Base.metadata.sorted_tables:
        if table.name not in existing:
            continue
        present = {index['name'] for index in inspector.get_indexes(table.name)}
        for index in table.indexes:
            if index.name in present:
                continue
            columns = ', '.join(column.name for column in index.columns)
            # PostgreSQL's IF NOT EXISTS does not survive concurrency: two
            # sessions racing on the same index collide in pg_class, which is
            # why this goes through the savepoint rather than relying on it.
            # An index is a performance concern, so a failure here is reported
            # but does not block startup.
            _migrate_tolerantly(connection, f'CREATE INDEX IF NOT EXISTS {index.name} ON {table.name} ({columns})',
                                f'create index {index.name}')
    if refused:
        detail = '; '.join(refused)
        if os.getenv('REVIEW_ALLOW_PARTIAL_SCHEMA', '').lower() in ('1', 'true', 'yes'):
            log.warning('database is missing %s column(s) and REVIEW_ALLOW_PARTIAL_SCHEMA is set; '
                        'queries using them will fail until they are migrated by hand: %s', len(refused), detail)
            return
        raise SchemaOutOfDate(
            f'数据库缺少 {len(refused)} 个无法自动添加的列，请手工迁移后重启：{detail}。'
            '如确需先启动，设置 REVIEW_ALLOW_PARTIAL_SCHEMA=1 跳过此检查（相关功能仍会报错）。')


def _schema_lock_timeout():
    try:
        return float(os.getenv('REVIEW_SCHEMA_LOCK_TIMEOUT', '60'))
    except ValueError:
        return 60.0


def init_db():
    with _schema_lock(timeout=_schema_lock_timeout()):
        _create_missing_tables()
        with engine.begin() as connection:
            _sync_schema(connection)
    for attempt in range(1, 4):
        try:
            with Session.begin() as db:
                if not db.get(Workspace, 1):
                    db.add(Workspace(id=1))
            return
        except IntegrityError as exc:
            # Another process seeded the singleton workspace row first.
            log.warning('workspace seeding raced (%s), retrying: %s', attempt, exc)
