import hashlib
import hmac
import os
import secrets
import time

from fastapi import Depends, HTTPException, Request
from sqlalchemy import select

from .db import AccessSession, LoginSession, Session, User


def hash_password(password: str) -> str:
    if len(password) < 10 or len(password) > 256:
        raise HTTPException(422, '密码长度需为 10–256 个字符')
    salt = secrets.token_bytes(16)
    value = hashlib.scrypt(password.encode(), salt=salt, n=16384, r=8, p=1)
    return salt.hex() + ':' + value.hex()


def verify_password(password, stored):
    try:
        salt, expected = stored.split(':')
        value = hashlib.scrypt(password.encode(), salt=bytes.fromhex(salt), n=16384, r=8, p=1)
        return hmac.compare_digest(value.hex(), expected)
    except (ValueError, TypeError):
        return False


def token_hash(value):
    return hashlib.sha256(value.encode()).hexdigest()


def db_session():
    with Session() as db:
        try:
            yield db
            db.commit()
        except BaseException:
            db.rollback()
            raise


def access_session(request: Request, db):
    token = request.cookies.get('review_access', '')
    session = db.get(AccessSession, token_hash(token)) if token else None
    user = db.get(User, session.user_id) if session and session.expires > int(time.time()) else None
    if not user or not user.active or user.password or user.role != 'member':
        raise HTTPException(401, '请输入访问口令进入知识库')
    return session, user


def current_user(request: Request, db=Depends(db_session, scope='function')):
    access, member = access_session(request, db)
    token = request.cookies.get('review_session', '')
    login = db.get(LoginSession, token_hash(token)) if token else None
    user = db.get(User, login.user_id) if login and login.expires > int(time.time()) else None
    if not user or not user.active or user.role != 'admin' or not user.password:
        login, user = access, member
    if request.method not in ('GET', 'HEAD', 'OPTIONS'):
        if not hmac.compare_digest(request.headers.get('x-csrf-token', ''), login.csrf):
            raise HTTPException(403, '会话校验失败，请刷新页面重试')
    request.state.login = login
    return user


def admin(user=Depends(current_user, scope='function')):
    if user.role != 'admin':
        raise HTTPException(403, '此操作仅限管理员')
    return user


def user_view(user):
    return {'id': user.id, 'username': user.username, 'name': user.name, 'role': user.role, 'active': user.active,
            'passcode_member': not bool(user.password)}

