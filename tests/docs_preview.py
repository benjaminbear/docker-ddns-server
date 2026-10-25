#!/usr/bin/env python3
"""Run a disposable, loopback-only UI fixture for documentation screenshots.
Usage: python3 tests/docs_preview.py [--image ddns-docs-preview] [--port 18084]
Requires Docker and Python cryptography. Never reads production data or contacts a CA.
Certificate and passkey states are illustrative fixtures, not real enrollments.
"""
import argparse
import datetime as dt
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import signal
import sqlite3
import subprocess
import tempfile
import time
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.x509.oid import NameOID

PRODUCTION = 'https://acme-v02.api.letsencrypt.org/directory'

def run(*args):
    return subprocess.check_output(args, text=True).strip()

def seed(root):
    db = sqlite3.connect(root / 'ddns.db')
    assert db.execute('select count(*) from hosts').fetchone()[0] == 0
    now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
    def stamp(delta=0): return (now + dt.timedelta(minutes=delta)).isoformat(sep=' ')
    def insert(table, **fields):
        names = ','.join(fields)
        return db.execute(f'insert into {table} ({names}) values ({",".join("?" for _ in fields)})', tuple(fields.values())).lastrowid
    hosts = []
    for label, zone, ip, ttl, minutes in [
        ('router','ddns.ws','192.0.2.10',60,-5),
        ('workstation','ddns.ws','2001:db8::20',300,-12),
        ('camera','dvr.bz','198.51.100.40',600,-18),
        ('recorder','dvr.bz','203.0.113.50',3600,-25),
        ('backup','ddns.ws','192.0.2.60',3600,-40),
    ]:
        hosts.append(insert('hosts', hostname=label,domain=zone,ip=ip,ttl=ttl,last_update=stamp(minutes),user_name=label,password='demo-device-password',created_at=stamp(-1440),updated_at=stamp(minutes)))
    insert('c_names', hostname='gateway',target_id=hosts[0],ttl=300,created_at=stamp(-1440))
    insert('c_names', hostname='video',target_id=hosts[3],ttl=600,created_at=stamp(-1440))
    for zone,name,kind,value,ttl in [
        ('ddns.ws','@','TXT','v=spf1 -all',3600),
        ('ddns.ws','@','MX','10 mail.example.net.',3600),
        ('ddns.ws','mail','A','192.0.2.25',3600),
        ('ddns.ws','@','CAA','0 issue "letsencrypt.org"',3600),
        ('ddns.ws','_sip._tcp','SRV','10 5 5060 sip.example.net.',600),
        ('dvr.bz','support','CNAME','help.example.net.',300),
    ]:
        insert('records',domain=zone,name=name,type=kind,value=value,ttl=ttl,managed='static')
    for i,host in enumerate(hosts):
        insert('logs',host_id=host,status=i!=3,message='No errors occurred' if i!=3 else 'DNS update temporarily unavailable',sent_ip=['192.0.2.10','2001:db8::20','198.51.100.40','203.0.113.50','192.0.2.60'][i],caller_ip='192.0.2.200',time_stamp=stamp(-5-i*7),created_at=stamp(-5-i*7),user_agent='DemoRouter/1.0')
    for i,label in enumerate(['camera.dvr.bz','router.ddns.ws','recorder.dvr.bz','backup.ddns.ws']):
        insert('failed_auths',hostname=label,ip_address=f'198.51.100.{80+i}',username='demo-device',password='example-incorrect-password',path='/nic/update',user_agent='DemoDDNS/1.0',timestamp=stamp(-10-i*6),created_at=stamp(-10-i*6))
    insert('failed_auths',hostname='',ip_address='203.0.113.90',username='guest',password='example-attempt',path='/@/login',user_agent='DemoBrowser/1.0',timestamp=stamp(-50),created_at=stamp(-50))
    insert('blocked_ips',ip_address='203.0.113.90',blocked_at=stamp(-50),blocked_until=stamp(7*1440),failure_count=3,is_permanent=False,last_attempt_at=stamp(-50),reason='Repeated failed admin sign-ins',created_at=stamp(-50))
    for ident,name in [('demo-laptop','Laptop passkey'),('demo-security-key','USB security key')]:
        insert('admin_passkeys',id=ident,name=name,credential=b'{}',provider='password',subject='fixture',created_at=stamp(-1440),last_used_at=stamp(-30))
    key = (root / 'cert.key').read_bytes()
    for domains in ['ddns.ws,*.ddns.ws','dvr.bz,*.dvr.bz','router.ddns.ws']:
        cert_id = insert('certificates',domains=domains,email='admin@example.test',directory=PRODUCTION,key_type='rsa2048',status='valid',auto_renew=True,not_after=stamp(90*1440),renew_at=stamp(60*1440),next_attempt=stamp(60*1440),created_at=stamp(-1440),updated_at=stamp(-30))
        private = rsa.generate_private_key(public_exponent=65537,key_size=2048)
        subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,'Documentation fixture')])
        cert = (x509.CertificateBuilder().subject_name(subject).issuer_name(subject).public_key(private.public_key()).serial_number(x509.random_serial_number()).not_valid_before(now-dt.timedelta(days=1)).not_valid_after(now+dt.timedelta(days=90)).add_extension(x509.SubjectAlternativeName([x509.DNSName(x) for x in domains.split(',')]),critical=False).sign(private,hashes.SHA256())).public_bytes(serialization.Encoding.PEM)
        pem = private.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption())
        nonce = os.urandom(12)
        sealed = nonce + AESGCM(key).encrypt(nonce,pem,f'certificate:{cert_id}'.encode())
        db.execute('update certificates set cert_pem=?,chain_pem=?,key_pem_enc=? where id=?',(cert,cert,sealed,cert_id))
        insert('events',certificate_id=cert_id,action='issue',result='Documentation fixture: successful issuance',created_at=stamp(-30))
    insert('certificates',domains='camera.dvr.bz',email='admin@example.test',directory=PRODUCTION,key_type='ec256',status='pending',auto_renew=False,next_attempt=stamp(60*1440),created_at=stamp(-1),updated_at=stamp(-1))
    insert('automations',id=1,enabled=True,scope='zones',email='admin@example.test',key_type='rsa2048',directory=PRODUCTION)
    db.commit(); db.close()

def stop_preview(signum, frame):
    raise KeyboardInterrupt

def main():
    signal.signal(signal.SIGTERM, stop_preview)
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image',default='ddns-docs-preview')
    parser.add_argument('--port',type=int,default=18084)
    args=parser.parse_args()
    root=tempfile.TemporaryDirectory(prefix='ddns-docs-')
    name='ddns-docs-'+secrets.token_hex(3)
    gateway=run('docker','network','inspect','bridge','--format','{{(index .IPAM.Config 0).Gateway}}')
    env={'TZ':'America/New_York','DDNS_TITLE':'w3K DDNS','DDNS_DOMAINS':'ddns.ws,dvr.bz','DDNS_PARENT_NS':'ns.example.net','DDNS_DEFAULT_TTL':'600','DDNS_SESSION_SECRET':'documentation-fixture-secret-at-least-32-bytes','DDNS_ADMIN_LOGIN':'fixture:fixture-password','DDNS_AUTH_ORIGIN':'https://admin.example.test','DDNS_GOOGLE_CLIENT_ID':'documentation-fixture.apps.googleusercontent.com','DDNS_GOOGLE_CLIENT_SECRET':'documentation-fixture-not-a-real-secret','DDNS_GOOGLE_ALLOWED_EMAILS':'admin@example.test','DDNS_TRUSTED_PROXIES':gateway+'/32','DDNS_ACME_DIRECTORY':'http://127.0.0.1:9/disabled'}
    command=['docker','run','-d','--name',name,'-p','127.0.0.1::8080','-v',root.name+':/root/database']
    for k,v in env.items(): command+=['-e',k+'='+v]
    try:
        run(*command,args.image,'/root/dyndns')
        backend=int(run('docker','port',name,'8080/tcp').rsplit(':',1)[1])
        for _ in range(60):
            try:
                c=http.client.HTTPConnection('127.0.0.1',backend,timeout=2);c.request('GET','/@/login');r=c.getresponse();r.read();c.close()
                if r.status==200: break
            except OSError: pass
            time.sleep(.25)
        seed(Path(root.name))
        class Proxy(BaseHTTPRequestHandler):
            def do_GET(self): self.forward()
            def do_POST(self): self.forward()
            def log_message(self,*args): pass
            def forward(self):
                headers=dict(self.headers);headers.update({'Host':'admin.example.test','X-Forwarded-Proto':'https','X-Forwarded-For':'192.0.2.200'})
                body=self.rfile.read(int(self.headers.get('Content-Length','0')))
                c=http.client.HTTPConnection('127.0.0.1',backend,timeout=15);c.request(self.command,self.path,body,headers);r=c.getresponse();data=r.read()
                self.send_response(r.status)
                for k,v in r.getheaders():
                    if k.lower() not in ('transfer-encoding','connection','content-length'): self.send_header(k,v)
                self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data);c.close()
        print(json.dumps({'url':f'http://127.0.0.1:{args.port}','database':root.name+'/ddns.db','container':name,'login':'fixture / fixture-password'}),flush=True)
        ThreadingHTTPServer(('127.0.0.1',args.port),Proxy).serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        root.cleanup()

if __name__=='__main__': main()
