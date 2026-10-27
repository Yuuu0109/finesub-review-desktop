"""Run the built site and durable worker locally with one command."""
import argparse
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=8765)
    args = parser.parse_args()
    if not (ROOT / 'frontend' / 'dist' / 'index.html').exists():
        raise SystemExit('请先在 frontend 运行 pnpm install 和 pnpm build')
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
            if os.name == 'nt':
                probe.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
            probe.bind((args.host, args.port))
    except OSError as exc:
        raise SystemExit(f'无法监听 {args.host}:{args.port}，请检查端口占用或使用 --port 指定其他端口。{exc}') from exc
    sys.path.insert(0, str(ROOT / 'backend'))
    from app.db import init_db
    init_db()
    children = []
    try:
        children.append(subprocess.Popen([sys.executable, '-m', 'app.worker'], cwd=ROOT / 'backend'))
        children.append(subprocess.Popen([sys.executable, '-m', 'uvicorn', 'app.main:app', '--host', args.host, '--port', str(args.port)], cwd=ROOT / 'backend'))
        print(f'FineSub 审核站：http://{args.host}:{args.port}', flush=True)
        while all(p.poll() is None for p in children):
            time.sleep(1)
        raise SystemExit('网站或任务进程已停止，请查看错误日志')
    except KeyboardInterrupt:
        pass
    finally:
        for process in children:
            if process.poll() is None:
                if os.name == 'nt':
                    # Windows venv launchers spawn another Python process.
                    # Terminate only this launcher's own child process tree.
                    subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                else:
                    process.terminate()
        for process in children:
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()


if __name__ == '__main__':
    main()
