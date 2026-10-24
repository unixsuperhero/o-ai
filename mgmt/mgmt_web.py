"""Loopback-only browser review over the existing session persistence contract."""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import secrets
from urllib.parse import parse_qs, urlsplit
import webbrowser


class Conflict(ValueError):
    pass


def serve(root, port, open_browser, sessions):
    root = root.expanduser().resolve()
    if not root.exists():
        raise ValueError(f'Review path does not exist: {root}')
    assets = Path(__file__).parent
    token = secrets.token_urlsafe(32)
    paths = {}

    def discover():
        candidates = [root] if root.is_file() else sorted(root.rglob('session.json'))
        entries, errors = [], []
        for candidate in candidates:
            path = candidate.resolve()
            if root.is_dir() and not path.is_relative_to(root):
                continue
            try:
                data = sessions.load(path)
                identity = hashlib.sha256(str(path).encode()).hexdigest()
                entry = dict(id=identity, title=data['title'], path=str(path),
                             total=len(data['items']),
                             selected=sum(bool(i['action']) for i in data['items']),
                             done=sum(i['status'] == 'done' for i in data['items']))
                paths[identity] = path
                entries.append(entry)
            except (OSError, ValueError, KeyError, TypeError) as error:
                errors.append(dict(path=str(path), error=str(error)))
        return dict(sessions=entries, errors=errors)

    def envelope(identity):
        path = paths[identity]
        raw = path.read_bytes()
        data = json.loads(raw)
        if data.get('version') != 1:
            raise ValueError('Unsupported session version')
        return dict(id=identity, path=str(path), revision=hashlib.sha256(raw).hexdigest(), data=data)

    class Handler(BaseHTTPRequestHandler):
        def setup(self):
            super().setup()
            self.connection.settimeout(10)

        def log_message(self, *_args):
            pass

        def respond(self, status, value, content_type='application/json; charset=utf-8'):
            body = json.dumps(value, ensure_ascii=False).encode() if content_type.startswith('application/json') else value
            self.send_response(status)
            self.send_header('Content-Type', content_type)
            self.send_header('Content-Length', str(len(body)))
            self.send_header('Cache-Control', 'no-store')
            self.send_header('X-Content-Type-Options', 'nosniff')
            self.send_header('Referrer-Policy', 'no-referrer')
            self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
            self.end_headers()
            self.wfile.write(body)

        def authorized(self, api=False):
            if self.headers.get('Host') != address:
                self.respond(403, dict(error='Open the printed loopback URL.'))
                return False
            if self.headers.get('Sec-Fetch-Site') == 'cross-site':
                self.respond(403, dict(error='Cross-site requests are not allowed.'))
                return False
            if api and not secrets.compare_digest(self.headers.get('X-Mgmt-Token', ''), token):
                self.respond(403, dict(error='Invalid review token. Reload this page.'))
                return False
            origin = self.headers.get('Origin')
            if origin and origin != url:
                self.respond(403, dict(error='Cross-origin requests are not allowed.'))
                return False
            return True

        def do_GET(self):
            route = urlsplit(self.path)
            if not self.authorized(route.path.startswith('/api/')):
                return
            try:
                if route.path == '/api/sessions':
                    self.respond(200, discover())
                elif route.path == '/api/session':
                    identity = parse_qs(route.query).get('id', [''])[0]
                    self.respond(200, envelope(identity))
                elif route.path in ('/', '/web.js', '/web.css'):
                    name, mime = {'/': ('web.html', 'text/html'), '/web.js': ('web.js', 'text/javascript'), '/web.css': ('web.css', 'text/css')}[route.path]
                    body = (assets / name).read_bytes()
                    if name == 'web.html':
                        body = body.replace(b'__MGMT_TOKEN__', token.encode())
                    self.respond(200, body, mime + '; charset=utf-8')
                else:
                    self.respond(404, dict(error='Not found'))
            except KeyError:
                self.respond(404, dict(error='Session not found. Refresh the session list.'))
            except (OSError, ValueError, TypeError) as error:
                self.respond(400, dict(error=str(error)))

        def do_POST(self):
            if not self.authorized(api=True):
                return
            if self.path != '/api/session':
                self.respond(404, dict(error='Not found'))
                return
            try:
                if self.headers.get('Content-Type', '').split(';')[0] != 'application/json':
                    raise ValueError('Expected application/json')
                size = int(self.headers.get('Content-Length', '0'))
                if not 0 < size <= 1024 * 1024:
                    raise ValueError('Request must be between 1 byte and 1 MiB')
                request = json.loads(self.rfile.read(size))
                if not isinstance(request, dict):
                    raise ValueError('Expected an object')
                identity = request.get('id')
                if not isinstance(identity, str) or identity not in paths:
                    self.respond(404, dict(error='Session not found'))
                    return
                number = request.get('number')
                if type(number) is not int:
                    raise ValueError('Item number must be an integer')
                if not all(isinstance(request.get(key), str) for key in ('revision', 'action', 'note')):
                    raise ValueError('Revision, action, and note must be strings')
                with sessions.locked(paths[identity]):
                    current = envelope(identity)
                    if current['revision'] != request['revision']:
                        raise Conflict('This session changed elsewhere. Your draft is preserved; reload saved data before editing again.')
                    data = current['data']
                    item = sessions.item_at(data, number)
                    if item['status'] in ('applying', 'done'):
                        raise Conflict('This item is active or completed and cannot be edited.')
                    sessions.change(data, number, action=request['action'], note=request['note'])
                    data.pop('reviewed_at', None)
                    sessions.save(paths[identity], data)
                    self.respond(200, envelope(identity))
            except Conflict as error:
                self.respond(409, dict(error=str(error)))
            except (ValueError, OSError, KeyError, TypeError, IndexError) as error:
                self.respond(400, dict(error=str(error)))

    discover()
    with ThreadingHTTPServer(('127.0.0.1', port), Handler) as server:
        address = f'127.0.0.1:{server.server_port}'
        url = f'http://{address}'
        print(f'Review workspace: {url}\nSession scope: {root}\nCtrl-C stops the server. Saved decisions remain on disk.', flush=True)
        if open_browser:
            webbrowser.open(url)
        server.serve_forever()
