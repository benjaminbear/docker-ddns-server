#!/usr/bin/env python3
"""Disposable BIND + Pebble end-to-end test. No production CA or production data.
Usage: python3 tests/features_smoke.py [image] [--keep]
Requires Docker, Python 3, and the official ghcr.io/letsencrypt/pebble image.
"""
import base64
import hashlib
import http.cookiejar
import json
import io
import zipfile
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def run(*args, **kw):
    return subprocess.check_output(args, text=True, **kw).strip()


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else 'ddns-features-test:local'
    name = 'ddns-feature-' + secrets.token_hex(4)
    pebble = name + '-ca'
    root = tempfile.TemporaryDirectory(prefix='ddns-feature-')
    trust = Path(root.name) / 'root.pem'
    helper = run('docker', 'create', 'ghcr.io/letsencrypt/pebble:latest')
    try:
        run('docker', 'cp', helper + ':/test/certs/pebble.minica.pem', str(trust))
    finally:
        run('docker', 'rm', helper)
    auth = 'fixture:{SHA}' + base64.b64encode(hashlib.sha1(b'fixture-password').digest()).decode()
    env = dict(os.environ, DDNS_SESSION_SECRET=secrets.token_hex(32), DDNS_ADMIN_LOGIN=auth)
    run('docker', 'run', '-d', '--name', name, '-p', '127.0.0.1::8080',
        '-v', str(trust) + ':/test-root.pem:ro',
        '-e', 'DDNS_SESSION_SECRET', '-e', 'DDNS_ADMIN_LOGIN',
        '-e', 'DDNS_DOMAINS=example.test', '-e', 'DDNS_PARENT_NS=ns.example.net',
        '-e', 'DDNS_DEFAULT_TTL=60', '-e', 'DDNS_ALLOW_WILDCARD=true',
        '-e', 'DDNS_ACME_DIRECTORY=https://localhost:14000/dir',
        '-e', 'LEGO_CA_CERTIFICATES=/test-root.pem',
        '-e', 'DDNS_ACME_RESOLVERS=127.0.0.1:53',
        '-e', 'DDNS_ALLOW_INSECURE_DOWNLOAD=true', image, env=env)
    run('docker', 'run', '-d', '--name', pebble, '--network', 'container:' + name,
        '-e', 'PEBBLE_VA_NOSLEEP=1', '-e', 'PEBBLE_AUTHZREUSE=0',
        'ghcr.io/letsencrypt/pebble:latest', '-config', '/test/config/pebble-config.json',
        '-dnsserver', '127.0.0.1:53')
    port = run('docker', 'port', name, '8080/tcp').rsplit(':', 1)[1]
    base = 'http://127.0.0.1:' + port
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def request(path, data=None):
        req = urllib.request.Request(base + path, None if data is None else urllib.parse.urlencode(data).encode())
        try:
            with opener.open(req, timeout=30) as response:
                payload=response.read()
                return response.status, payload if path.endswith(".zip") else payload.decode()
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode()

    def csrf(path='/@/records'):
        status, body = request(path)
        assert status == 200, (status, body)
        return re.search(r'name="csrf" value="([^"]+)"', body).group(1)

    def dns(name_, kind):
        return run('docker', 'exec', name, 'dig', '+short', '@127.0.0.1', name_ + '.example.test', kind)

    def record(label, kind, values, action='save'):
        return request('/@/records', dict(csrf=csrf(), zone='example.test', name=label, type=kind, ttl=60, values=values, action=action))

    def wait_cert():
        for _ in range(90):
            status, state = request('/@/certs/status')
            assert status == 200
            rows = json.loads(state)
            assert all(set(row) == {'id', 'status'} for row in rows)
            if not any(row['status'] in ('pending', 'renewing') for row in rows):
                status, body = request('/@/certs')
                assert '>valid<' in body, body
                return body
            time.sleep(1)
        raise AssertionError('certificate timeout')

    try:
        for _ in range(40):
            try:
                if request('/ping')[0] == 200:
                    break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(1)
        else:
            raise AssertionError('app startup failed')
        request('/@/login', dict(username='fixture', password='fixture-password'))
        assert request('/@/records', dict(zone='example.test'))[0] in (400, 403), 'CSRF bypass'
        cases = [('static', 'A', '192.0.2.1'), ('static', 'AAAA', '2001:db8::1'),
                 ('alias', 'CNAME', 'static.example.test'), ('@', 'MX', '10 mail.example.net\n20 backup.example.net'),
                 ('@', 'TXT', 'v=spf1 -all\nsecond "quoted" value'), ('@', 'CAA', '0 issue letsencrypt.org'),
                 ('_sip._tcp', 'SRV', '10 5 5060 static.example.test'), ('sub', 'NS', 'ns.example.net')]
        for label, kind, values in cases:
            status, body = record(label, kind, values)
            assert status == 200, (label, kind, body)
            query_label = '' if label == '@' else label
            qname = 'example.test' if not query_label else query_label + '.example.test'
            result = run('docker', 'exec', name, 'dig', '+short', '@127.0.0.1', qname, kind)
            if kind == 'NS':
                result = run('docker', 'exec', name, 'dig', '+noall', '+authority', '@127.0.0.1', qname, kind)
            assert result, (label, kind)
        assert record('alias', 'TXT', 'conflict')[0] == 400
        assert record('@', 'CNAME', 'static.example.test')[0] == 400
        assert record('_acme-challenge', 'TXT', 'reserved')[0] == 400
        status, body = request('/@/hosts/add', dict(hostname='router', domain='example.test', ip='192.0.2.2', ttl=60, username='router', password='fixture-password'))
        assert status == 200, body
        host_id = json.loads(body)['ID']
        assert record('router', 'TXT', 'keep-on-host-delete')[0] == 200
        assert record('router', 'A', '192.0.2.9')[0] == 400
        assert request('/@/hosts/delete/' + str(host_id))[0] == 200
        assert dns('router', 'TXT') == '"keep-on-host-delete"'
        assert dns('router', 'A') == ''
        long_txt = 'x' * 300 + '"\\unicode-é'
        assert record('long', 'TXT', long_txt)[0] == 200
        assert '" "' in dns('long', 'TXT'), 'TXT not chunked'
        for label, kind, values in cases:
            assert record(label, kind, '', 'delete')[0] == 200
        # Preserve a TXT value created outside the app during both wildcard challenges.
        subprocess.run(['docker', 'exec', '-i', name, 'nsupdate'], input='server localhost\nzone example.test\nupdate add _acme-challenge.example.test 60 TXT "external-value"\nsend\n', text=True, check=True)
        for key_type in ('ec256', 'rsa2048'):
            status, body = request('/@/certs', dict(csrf=csrf('/@/certs'), domains='*.example.test,example.test', email='fixture@example.test', key_type=key_type, terms='yes', auto_renew='yes', staging='yes'))
            assert status == 200, body
            body = wait_cert()
            cert_id = re.search(r'/@/certs/(\d+)/download/cert.pem', body).group(1)
            parts = {}
            for part in ('cert.pem', 'chain.pem', 'fullchain.pem', 'privkey.pem'):
                status, content = request('/@/certs/' + cert_id + '/download/' + part)
                assert status == 200 and content.startswith('-----BEGIN '), (part, status)
                parts[part] = content
            assert parts['fullchain.pem'] == parts['cert.pem'] + parts['chain.pem']
            status, package = request('/@/certs/' + cert_id + '/download/package.zip')
            assert status == 200
            with zipfile.ZipFile(io.BytesIO(package)) as bundle:
                assert set(bundle.namelist()) == set(parts)
                for part, content in parts.items():
                    assert bundle.read(part).decode() == content

            assert dns('_acme-challenge', 'TXT') == '"external-value"', 'challenge cleanup lost external TXT'
            old_key = parts['privkey.pem']
            assert request('/@/certs/' + cert_id + '/renew', dict(csrf=csrf('/@/certs')))[0] == 200
            wait_cert()
            assert request('/@/certs/' + cert_id + '/download/privkey.pem')[1] != old_key
            assert request('/@/certs/' + cert_id + '/revoke', dict(csrf=csrf('/@/certs')))[0] == 200
            assert '>revoked<' in request('/@/certs')[1]
        status, body = request('/@/certs/automation', dict(csrf=csrf('/@/certs'), enabled='yes', scope='zones', email='fixture@example.test', key_type='ec256', staging='yes', terms='yes'))
        assert status == 200, body
        wait_cert()
        print('Automatic zone wildcard issuance passed.')
        print('Feature smoke passed: all eight record types, multi-value TXT/MX, CNAME conflicts, legacy ownership, CSRF, long TXT, wildcard+apex EC/RSA issuance, renewal/key rotation, PEM downloads, revocation, challenge cleanup.')
        if '--keep' in sys.argv:
            print('Preview:', base, 'container:', name, 'CA:', pebble, 'fixture credentials: fixture / fixture-password')
            input('Press Enter to remove disposable preview containers: ')
    except Exception:
        print(run('docker', 'logs', name, stderr=subprocess.STDOUT)[-8000:])
        print(run('docker', 'logs', pebble, stderr=subprocess.STDOUT)[-4000:])
        raise
    finally:
        run('docker', 'rm', '-f', pebble, name)
        root.cleanup()

if __name__ == '__main__':
    main()
