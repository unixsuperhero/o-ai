"""Run with python3 -m unittest discover -s mgmt -p 'test_*.py'."""
import fcntl
import http.client
import json
from pathlib import Path
import re
import select
import subprocess
import sys
import tempfile
import unittest
from urllib.parse import urlsplit


class BrowserReviewTest(unittest.TestCase):
    def test_decisions_persist_without_lost_updates_or_unauthorized_writes(self):
        tool = Path(__file__).with_name('mgmt.py')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'outline.md'
            source.write_text('Keep this source unchanged.\n')
            inventory = root / 'inventory.json'
            inventory.write_text(json.dumps(dict(title='Review', scope=str(source), default_action='remove', items=[
                dict(title='First', source=str(source), details='Original requirement'),
                dict(title='Second', source=str(source), details='Another requirement'),
            ])))
            session = root / 'session.json'
            subprocess.run([sys.executable, str(tool), 'init', str(session), str(inventory)], capture_output=True, timeout=10, check=True)
            process = subprocess.Popen([sys.executable, str(tool), 'serve', str(root), '--port', '0', '--no-open'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                self.assertTrue(select.select([process.stdout], [], [], 10)[0], 'Server startup timed out')
                line = process.stdout.readline().decode()
                address = urlsplit(line.removeprefix('Review workspace: ').strip())
                self.assertEqual(address.hostname, '127.0.0.1')

                def request(method, path, body=None, headers=None):
                    connection = http.client.HTTPConnection(address.hostname, address.port, timeout=10)
                    try:
                        connection.request(method, path, json.dumps(body) if body is not None else None, headers or {})
                        response = connection.getresponse()
                        return response.status, response.read().decode()
                    finally:
                        connection.close()

                status, html = request('GET', '/')
                self.assertEqual(status, 200)
                token = re.search(r'name="mgmt-token" content="([^"]+)"', html).group(1)
                auth = {'X-Mgmt-Token': token, 'Content-Type': 'application/json'}
                self.assertEqual(request('GET', '/api/sessions')[0], 403)
                self.assertEqual(request('GET', '/', headers={'Host': 'attacker.example'})[0], 403)
                status, listing = request('GET', '/api/sessions', headers=auth)
                self.assertEqual(status, 200)
                identity = json.loads(listing)['sessions'][0]['id']
                _, text = request('GET', '/api/session?id=' + identity, headers=auth)
                original = json.loads(text)
                change = dict(id=identity, revision=original['revision'], number=1, action='remove', note='Preserve the example.')
                self.assertEqual(request('POST', '/api/session', change, {**auth, 'Origin': 'https://attacker.example'})[0], 403)
                self.assertEqual(request('POST', '/api/session', {**change, 'number': True}, auth)[0], 400)
                status, text = request('POST', '/api/session', change, auth)
                self.assertEqual(status, 200)
                saved = json.loads(text)
                self.assertEqual((saved['data']['items'][0]['action'], saved['data']['items'][0]['note']), ('remove', 'Preserve the example.'))
                self.assertEqual(request('POST', '/api/session', {**change, 'note': 'Stale overwrite'}, auth)[0], 409)
                self.assertEqual(json.loads(session.read_text())['items'][0]['note'], 'Preserve the example.')
                with open(str(session) + '.lock', 'a') as lock:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    self.assertNotEqual(request('POST', '/api/session', {**change, 'revision': saved['revision']}, auth)[0], 200)
                note_only = {**change, 'revision': saved['revision'], 'number': 2, 'action': '', 'note': 'Personal reminder'}
                status, _ = request('POST', '/api/session', note_only, auth)
                self.assertEqual(status, 200)
                queue = subprocess.run([sys.executable, str(tool), 'apply', str(session)], capture_output=True, text=True, timeout=10, check=True)
                self.assertEqual([(i['number'], i['action'], i['note']) for i in json.loads(queue.stdout)['items']], [(1, 'remove', 'Preserve the example.')])
                _, text = request('GET', '/api/session?id=' + identity, headers=auth)
                active = json.loads(text)
                self.assertEqual(request('POST', '/api/session', {**change, 'revision': active['revision']}, auth)[0], 409)
                self.assertEqual(source.read_text(), 'Keep this source unchanged.\n')
                self.assertEqual(session.stat().st_mode & 0o777, 0o600)
            finally:
                process.terminate()
                process.communicate(timeout=10)


if __name__ == '__main__':
    unittest.main()
