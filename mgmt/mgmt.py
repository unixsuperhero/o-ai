#!/usr/bin/env python3
"""Persistent human review, followed by an assistant-executed action queue."""
import argparse
import contextlib
import datetime
import fcntl
import json
import os
from pathlib import Path
import shlex
import sys
import tempfile


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def save(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix='.' + path.name, dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream, indent=2, ensure_ascii=False)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


@contextlib.contextmanager
def locked(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(str(path) + '.lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('This session is open elsewhere. Close its review first.')
        yield


def load(path):
    data = json.loads(path.read_text())
    if data.get('version') != 1:
        raise ValueError('Unsupported session version')
    return data


def item_at(data, number):
    if number < 1 or number > len(data['items']):
        raise ValueError('Item number out of range')
    return data['items'][number - 1]


def display(data):
    print('\n' + data['title'])
    print('Scope: ' + data['scope'])
    for number, item in enumerate(data['items'], 1):
        selected = 'x' if item['action'] else ' '
        print(f"{number:3}. [{selected}] {item['title']} [{item['status']}]")
        if item['action']:
            print('         Action: ' + item['action'])
        if item['note']:
            print('         Note: ' + item['note'])


def change(data, number, action=None, note=None):
    item = item_at(data, number)
    if item['status'] in ('applying', 'done'):
        raise ValueError('Active/completed items are locked; record a result or create a new session')
    if action is not None:
        item['action'] = action
    if note is not None:
        item['note'] = note
    item['status'] = 'pending'
    item.pop('result', None)
    data['updated_at'] = now()


def review(path, data):
    display(data)
    print('\nCommands: show N | check N | uncheck N | action N TEXT | note N TEXT')
    print('          list | done (finish review) | quit (resume later)')
    print('Changes save immediately. Checking uses the session default action.')
    while True:
        try:
            text = input('mgmt> ').strip()
        except EOFError:
            return
        try:
            parts = text.split(maxsplit=2)
            if not parts:
                continue
            cmd = parts[0]
            if cmd in ('quit', 'q'):
                return
            if cmd == 'done':
                data['reviewed_at'] = now()
                save(path, data)
                print('Review saved. Ask your assistant to apply this session: ' + str(path))
                return
            if cmd == 'list':
                display(data)
                continue
            if cmd not in ('show', 'check', 'uncheck', 'action', 'note'):
                raise ValueError('Unknown command')
            if len(parts) < 2:
                raise ValueError('Supply an item number')
            number = int(parts[1])
            if cmd == 'show':
                print(json.dumps(item_at(data, number), indent=2, ensure_ascii=False))
                continue
            if cmd in ('action', 'note') and len(parts) < 3:
                raise ValueError('Supply text (use note N - to clear a note)')
            if cmd == 'note':
                change(data, number, note='' if parts[2] == '-' else parts[2])
            else:
                action = data['default_action'] if cmd == 'check' else '' if cmd == 'uncheck' else parts[2]
                change(data, number, action=action)
            save(path, data)
            print('Saved.')
        except (ValueError, IndexError) as error:
            print('Error: ' + str(error))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    init = sub.add_parser('init', help='Create a session from an assistant-generated inventory JSON')
    init.add_argument('session', type=Path)
    init.add_argument('inventory', type=Path)
    for name in ('review', 'status', 'apply'):
        command = sub.add_parser(name)
        command.add_argument('session', type=Path)
    result = sub.add_parser('result', help='Record an executor result; never executes actions itself')
    result.add_argument('session', type=Path)
    result.add_argument('number', type=int)
    result.add_argument('outcome', choices=('done', 'failed'))
    result.add_argument('receipt', help='What changed and verification, or the failure/recovery details')
    web = sub.add_parser('serve', help='Open the browser review workspace (loopback only)')
    web.add_argument('path', nargs='?', type=Path, default=Path.home() / '.local/state/mgmt',
                     help='Session JSON or directory containing sessions')
    web.add_argument('--port', type=int, default=8765, help='Local port (0 chooses an available port)')
    web.add_argument('--no-open', action='store_true', help='Print the URL without opening a browser')
    args = parser.parse_args()
    if args.command == 'serve':
        from mgmt_web import serve
        if not 0 <= args.port <= 65535:
            raise ValueError('Port must be between 0 and 65535')
        serve(args.path, args.port, not args.no_open, sys.modules[__name__])
        return
    path = args.session.expanduser().resolve()
    with locked(path):
        if args.command == 'init':
            if path.exists():
                raise ValueError('Session already exists; refusing to overwrite choices')
            data = json.loads(args.inventory.read_text())
            for field in ('title', 'scope', 'default_action'):
                if not isinstance(data.get(field), str) or not data[field].strip():
                    raise ValueError(f'Inventory requires a nonempty {field}')
            if not isinstance(data.get('items'), list) or not data['items']:
                raise ValueError('Inventory requires a nonempty items array')
            items = []
            for item in data['items']:
                for field in ('title', 'source', 'details'):
                    if not isinstance(item.get(field), str) or not item[field].strip():
                        raise ValueError(f'Every item requires a nonempty {field}')
                items.append({key: item[key] for key in ('title', 'source', 'details')})
                items[-1].update(action='', note='', status='pending')
            data = {key: data[key] for key in ('title', 'scope', 'default_action')}
            data.update(version=1, items=items, created_at=now(), updated_at=now())
            save(path, data)
            print('Created ' + str(path))
            print('Review: ' + shlex.join([sys.executable, str(Path(__file__).resolve()), 'serve', str(path)]))
            return
        data = load(path)
        if args.command == 'review':
            review(path, data)
        elif args.command == 'status':
            print(json.dumps(data, indent=2, ensure_ascii=False))
        elif args.command == 'apply':
            if any(item['status'] == 'applying' for item in data['items']):
                raise ValueError('Unresolved applying items: inspect actual effects and record results before retrying')
            queue = []
            for number, item in enumerate(data['items'], 1):
                if item['action'] and item['status'] != 'done':
                    item['status'] = 'applying'
                    queue.append(dict(number=number, **item))
            data['updated_at'] = now()
            save(path, data)
            print(json.dumps({
                'mode': 'apply', 'session': str(path), 'scope': data['scope'],
                'executor': 'The assistant using the mgmt skill must execute these actions, verify them, and record each result. This command does not modify source material.',
                'items': queue,
            }, indent=2, ensure_ascii=False))
        else:
            item = item_at(data, args.number)
            if item['status'] != 'applying':
                raise ValueError('Only an applying item can receive a result')
            if not args.receipt.strip():
                raise ValueError('A nonempty result receipt is required')
            item.update(status=args.outcome, result=args.receipt, completed_at=now())
            data['updated_at'] = now()
            save(path, data)
            print(f'Item {args.number}: {args.outcome}')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError) as error:
        print('Error: ' + str(error), file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print('\nStopped. Previously saved choices are retained.', file=sys.stderr)
        sys.exit(130)
