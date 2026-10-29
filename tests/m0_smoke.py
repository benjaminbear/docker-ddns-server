#!/usr/bin/env python3
"""M0 API/BIND smoke test in a disposable, unpublished container.
Usage: python3 tests/m0_smoke.py [image]; requires Docker and a built DDNS image.
Only fixture credentials and example.test records are used; no host mounts.
"""
import base64
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.parse


def run(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def main():
    image = sys.argv[1] if len(sys.argv) > 1 else "ddns-m0-test:local"
    name = "ddns-m0-smoke-" + secrets.token_hex(4)
    env = dict(os.environ, DDNS_SESSION_SECRET=secrets.token_hex(32))
    run("docker", "run", "-d", "--name", name, "-e", "DDNS_SESSION_SECRET",
        "-e", "DDNS_DOMAINS=example.test", "-e", "DDNS_PARENT_NS=ns.example.net",
        "-e", "DDNS_DEFAULT_TTL=60", "-e", "DDNS_ALLOW_WILDCARD=true", image, env=env)

    def request(path, data=None, auth=None, headers=()):
        args = ["docker", "exec", name, "curl", "-sS", "--max-time", "5", "-w", "\n%{http_code}"]
        if data is not None:
            args += ["--data", urllib.parse.urlencode(data)]
        if auth is not None:
            args += ["-u", auth]
        for header in headers:
            args += ["-H", header]
        output = run(*args, "http://127.0.0.1:8080" + path, stderr=subprocess.DEVNULL)
        body, status = output.rsplit("\n", 1)
        return int(status), body

    def dns(host, kind):
        return run("docker", "exec", name, "dig", "+short", "@127.0.0.1", host + ".example.test", kind)

    try:
        for _ in range(30):
            try:
                if request("/ping")[0] == 200:
                    break
            except subprocess.CalledProcessError:
                pass
            time.sleep(1)
        else:
            raise AssertionError("app did not start")
        # A malformed public-IP service response must not mask the tests below.
        assert run("docker", "exec", name, "named-checkzone", "example.test", "/var/cache/bind/example.test.zone").endswith("OK")
        status, body = request("/@/hosts/add", dict(hostname="router", domain="example.test", ip="203.0.113.5", ttl=60, username="fixture", password="fixture-password"))
        assert status == 200, (status, body)
        host_id = json.loads(body)["ID"]
        assert dns("router", "A") == "203.0.113.5"
        for path in ("/update", "/nic/update", "/v2/update", "/v3/update"):
            assert request(path + "?hostname=router.example.test&myip=203.0.113.6", auth="fixture:fixture-password") == (200, "good\n")
        assert dns("router", "A") == "203.0.113.6"
        for auth in (":fixture-password", "fixture:", ":"):
            assert request("/nic/update?hostname=router.example.test", auth=auth)[0] == 401
        # Untrusted forwarding headers cannot control the implicit update IP.
        assert request("/nic/update?hostname=router.example.test", auth="fixture:fixture-password", headers=("X-Real-IP: 203.0.113.99", "X-Forwarded-For: 203.0.113.99"))[0] == 200
        assert dns("router", "A") == "127.0.0.1"
        assert request("/nic/update?hostname=router.example.test&myip=2001:db8::5", auth="fixture:fixture-password")[0] == 200
        assert dns("router", "AAAA") == "2001:db8::5"
        for target in ("1 OR 1=1", "id = 1"):
            assert request("/@/cnames/add", dict(hostname="bad", ttl=60, target_id=target))[0] == 400
        status, body = request("/@/cnames/add", dict(hostname="alias", ttl=60, target_id=host_id))
        assert status == 200, (status, body)
        assert dns("alias", "CNAME") == "router.example.test."
        assert dns("*.alias", "CNAME") == "router.example.test."
        # Failed DNS deletion must leave SQL rows available for a successful retry.
        run("docker", "exec", name, "service", "named", "stop")
        assert request("/@/hosts/delete/" + str(host_id))[0] == 400
        run("docker", "exec", name, "service", "named", "start")
        assert request("/@/hosts/delete/" + str(host_id))[0] == 200
        for host, kind in (("router", "A"), ("router", "AAAA"), ("alias", "CNAME"), ("*.alias", "CNAME"), ("*.router", "A")):
            assert dns(host, kind) == "", (host, kind)
        logs = run("docker", "logs", name, stderr=subprocess.STDOUT)
        assert base64.b64encode(b"fixture:fixture-password").decode() not in logs
        assert "fixture-password" not in logs
        print("M0 smoke passed: four HTTP APIs, IPv4/IPv6, proxy spoofing, SQL input, BIND CNAME/wildcard cascade, log redaction")
    finally:
        run("docker", "rm", "-f", name)


if __name__ == "__main__":
    main()
