#!/usr/bin/env python3
"""Run installer failure scenarios using only a temporary filesystem and mocks."""
import hashlib
import io
import json
import os
import pathlib
import subprocess
import tarfile
import tempfile
import unittest
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
NODE = b'''#!/usr/bin/env bash
set -e
case "$1" in
version) echo 'Remnawave Node (Go) vtest' ;;
core) printf '#!/bin/sh\\nexit 0\\n' > "$(dirname "$0")/rw-core"; chmod +x "$(dirname "$0")/rw-core" ;;
status) [ "${MOCK_READY:-1}" = 1 ] ;;
esac
'''
MOCK = '''#!/usr/bin/env python3
import json,os,pathlib,sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
root=pathlib.Path(os.environ['MOCK_ROOT'])
with open(root/'events','a') as log:log.write(name+' '+' '.join(args)+'\\n')
if name=='id':print('0')
elif name=='uname':print('x86_64')
elif name=='curl':
 url=next(a for a in args if a.startswith('https:'))
 asset=url.rsplit('/',1)[1]
 if asset=='checksums.txt' and '/geocheck/' in url:asset='geo-checksums.txt'
 target=args[args.index('-o')+1]
 pathlib.Path(target).write_bytes((root/'assets'/asset).read_bytes())
elif name=='systemctl':
 if args==['restart','remnanode'] and os.environ.get('MOCK_RESTART_FAIL')=='1':sys.exit(1)
elif name=='docker':
 if args[0]=='ps':print('old-node\\tremnawave/node:3.4.2')
 elif args[0]=='inspect':
  if '-f' in args:print('true')
  else:print(json.dumps([{'Config':{'Env':['SECRET_KEY=test','NODE_PORT=3000','NFTABLES_LOGGING=false']},'HostConfig':{'RestartPolicy':{'Name':'unless-stopped','MaximumRetryCount':0}}}]))
'''

class ScriptTests(unittest.TestCase):
    def test_shell_syntax(self):
        for path in [ROOT/'migrate.sh', *sorted((ROOT/'scripts').glob('*.sh'))]:
            subprocess.run(['bash', '-n', str(path)], check=True)

    def test_portable_asn(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory)/'index.zip'
            subprocess.run(['python3',str(ROOT/'scripts/pack-asn.py'),str(archive)],input=b'{"asn":64500,"ipv4":["192.0.2.0/24"],"ipv6":["2001:db8::/32"]}\n',check=True)
            with zipfile.ZipFile(archive) as result:
                self.assertEqual(json.loads(result.read('64500.json'))['ipv4'], ['192.0.2.0/24'])

    def run_installer(self, readiness='1', restart_failure='0', twice=False):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            (root/'assets').mkdir()
            (root/'bin').mkdir()
            for name in ['id','uname','docker','curl','systemctl']:
                script=root/'bin'/name
                script.write_text(MOCK)
                script.chmod(0o755)
            assets=root/'assets'
            (assets/'remnanode-linux-amd64').write_bytes(NODE)
            with zipfile.ZipFile(assets/'asn-prefixes.zip','w') as archive:
                archive.writestr('64500.json','{"ipv4":[],"ipv6":[]}')
            with tarfile.open(assets/'geocheck_linux_amd64.tar.gz','w:gz') as archive:
                entry=tarfile.TarInfo('geocheck');entry.mode=0o755;content=b'#!/bin/sh\nexit 0\n';entry.size=len(content);archive.addfile(entry,io.BytesIO(content))
            checksums=''.join(hashlib.sha256(file.read_bytes()).hexdigest()+'  '+file.name+'\n' for file in assets.iterdir())
            (assets/'checksums.txt').write_text(checksums)
            (assets/'geo-checksums.txt').write_text(checksums)
            env=dict(os.environ,PATH=str(root/'bin')+':'+os.environ['PATH'],MOCK_ROOT=str(root),MOCK_READY=readiness,MOCK_RESTART_FAIL=restart_failure,REMNANODE_INSTALL_DIR=str(root/'install'),REMNANODE_PREFIX=str(root/'prefix'),REMNANODE_SYSTEMD_DIR=str(root/'units'))
            result=subprocess.run(['bash',str(ROOT/'migrate.sh')],env=env,capture_output=True,text=True)
            if twice and result.returncode==0:
                result=subprocess.run(['bash',str(ROOT/'migrate.sh')],env=env,capture_output=True,text=True)
            events=(root/'events').read_text()
            if readiness=='1' and restart_failure=='0':
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                self.assertIn('Migration is ready',result.stdout)
                self.assertIn('NFTABLES_LOGGING=false',(root/'install/.env').read_text())
                self.assertEqual((root/'install/.env').stat().st_mode&0o777,0o600)
            else:
                self.assertNotEqual(result.returncode,0)
                self.assertIn('docker update --restart=unless-stopped old-node',events)
                self.assertIn('docker start old-node',events)
                self.assertNotIn('Migration is ready',result.stdout)
    def test_migration_success_and_rerun(self):self.run_installer(twice=True)
    def test_migration_readiness_rollback(self):self.run_installer(readiness='0')
    def test_migration_restart_failure_rollback(self):self.run_installer(restart_failure='1')

if __name__=='__main__':unittest.main()
