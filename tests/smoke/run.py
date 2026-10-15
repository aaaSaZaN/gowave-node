#!/usr/bin/env python3
"""Exercise the real HTTPS control plane and core in an isolated container."""
import base64
import gzip
import json
import os
import pathlib
import signal
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request

mode = sys.argv[1] if len(sys.argv) > 1 else 'native'
candidate = pathlib.Path(os.environ.get('REMNANODE_SMOKE_CANDIDATE', '/candidate'))
root = pathlib.Path(tempfile.mkdtemp(prefix='node-smoke-'))
processes = []
def command(*args):
    return subprocess.check_output(args, stderr=subprocess.DEVNULL)
def free_port():
    with socket.socket() as connection:
        connection.bind(('127.0.0.1', 0))
        return connection.getsockname()[1]
def encode(data):
    return base64.urlsafe_b64encode(data).rstrip(b'=')
try:
    command('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(root/'ca.key'), '-out', str(root/'ca.crt'), '-subj', '/CN=SmokeCA', '-days', '1', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign')
    command('openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(root/'node.key'), '-out', str(root/'node.csr'), '-subj', '/CN=localhost')
    (root/'extensions').write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:localhost,IP:127.0.0.1\nauthorityKeyIdentifier=keyid,issuer\nsubjectKeyIdentifier=hash\n')
    command('openssl', 'x509', '-req', '-in', str(root/'node.csr'), '-CA', str(root/'ca.crt'), '-CAkey', str(root/'ca.key'), '-CAcreateserial', '-out', str(root/'node.crt'), '-days', '1', '-extfile', str(root/'extensions'))
    public = command('openssl', 'rsa', '-in', str(root/'node.key'), '-pubout').decode()
    payload = dict(caCertPem=(root/'ca.crt').read_text(), nodeCertPem=(root/'node.crt').read_text(), nodeKeyPem=(root/'node.key').read_text(), jwtPublicKey=public)
    token_body = encode(b'{"alg":"RS256","typ":"JWT"}') + b'.' + encode(json.dumps({'exp': int(time.time())+600}).encode())
    signing = subprocess.run(['openssl','dgst','-sha256','-sign',str(root/'node.key')],input=token_body,capture_output=True,check=True).stdout
    token = (token_body+b'.'+encode(signing)).decode()
    port = free_port()
    environment = dict(os.environ, SECRET_KEY=base64.b64encode(json.dumps(payload).encode()).decode(), NODE_PORT=str(port), REMNANODE_RUNTIME_DIR=str(root/'run'), XTLS_API_SOCKET_PATH=str(root/'api.sock'), INTERNAL_SOCKET_PATH='smoke-'+root.name, INTERNAL_REST_TOKEN=os.urandom(32).hex(), XRAY_S6_SERVICE_DIR=str(root/'no-s6'), XRAY_CORE_PATH=str(candidate/'rw-core'))
    if mode == 's6':
        service = root/'services'/'xray'
        service.mkdir(parents=True)
        (service/'down').touch()
        script = '#!/bin/sh\n\nexec 2>&1\nexec /usr/local/bin/rw-core \\\n    -config @"${INTERNAL_SOCKET_PATH}":/internal/get-config?token="${INTERNAL_REST_TOKEN}" \\\n    -format json\n'
        (service/'run').write_text(script)
        (service/'run').chmod(0o755)
        processes.append(subprocess.Popen(['s6-svscan',str(root/'services')],env=environment))
        deadline = time.monotonic()+10
        while not (service/'supervise'/'control').exists():
            if time.monotonic()>deadline: raise TimeoutError('s6 control missing')
            time.sleep(.05)
        environment['XRAY_S6_SERVICE_DIR'] = str(service)
    processes.append(subprocess.Popen([str(candidate/'remnanode')],env=environment,cwd=candidate))
    context = ssl.create_default_context(cafile=str(root/'ca.crt'))
    context.check_hostname = False
    context.load_cert_chain(str(root/'node.crt'),str(root/'node.key'))
    def call(path, body=None):
        data = gzip.compress(json.dumps(body).encode()) if body is not None else None
        request = urllib.request.Request('https://127.0.0.1:'+str(port)+'/node/'+path,data=data,headers={'Authorization':'Bearer '+token, 'Content-Type':'application/json', 'Content-Encoding':'gzip'} if data else {'Authorization':'Bearer '+token})
        with urllib.request.urlopen(request,context=context,timeout=55) as response:
            return json.load(response)['response']
    deadline = time.monotonic()+30
    while True:
        try:
            call('xray/healthcheck')
            break
        except Exception:
            if time.monotonic()>deadline: raise
            time.sleep(.1)
    configuration = {'internals': {'hashes': {'emptyConfig':'smoke','inbounds':[{'tag':'in','hash':'0000000000000000','usersCount':0}]}}, 'xrayConfig': {'inbounds':[{'tag':'in','listen':'127.0.0.1','port':free_port(),'protocol':'vless','settings':{'clients':[],'decryption':'none'}}], 'outbounds':[{'tag':'direct','protocol':'freedom'}]}}
    started = call('xray/start',configuration)
    assert started['isStarted'], started
    user_id = '00000000-0000-4000-8000-000000000001'
    added = call('handler/add-user',{'data':[{'type':'vless','tag':'in','username':'00123','uuid':user_id,'flow':''}],'hashData':{'vlessUuid':user_id}})
    assert added['success'], added
    assert isinstance(call('stats/get-users-stats',{'reset':True})['users'], list)
    removed = call('handler/remove-user',{'username':'00123','hashData':{'vlessUuid':user_id}})
    assert removed['success'], removed
    if mode == 'native':
        def rss(pid):
            return next(line.split()[1] for line in pathlib.Path(f'/proc/{pid}/status').read_text().splitlines() if line.startswith('VmRSS:'))
        node_pid = processes[-1].pid
        children = set()
        for task in pathlib.Path(f'/proc/{node_pid}/task').iterdir():
            try: children.update((task/'children').read_text().split())
            except FileNotFoundError: pass
        print(json.dumps({'nodeRSSKiB': int(rss(node_pid)), 'coreRSSKiB': [int(rss(pid)) for pid in children]}), flush=True)
    assert call('xray/stop')['isStopped']
    print(mode+' HTTPS, compressed config, core readiness, user mutation, stats and shutdown passed',flush=True)
finally:
    for process in reversed(processes):
        process.terminate()
        try: process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()

    shutil.rmtree(root, ignore_errors=True)
