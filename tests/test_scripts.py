#!/usr/bin/env python3
"""Run installer scenarios using only a temporary filesystem and mocks."""
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
INSTALLER = ROOT/'install.sh'
NODE = b'''#!/bin/sh
set -e
case "$1" in
version) echo 'Remnawave Node (Go) vtest' ;;
core) printf '#!/bin/sh\\nexit 0\\n' > "$(dirname "$0")/rw-core"; chmod +x "$(dirname "$0")/rw-core" ;;
service) bash "$REMNANODE_SERVICE_INSTALLER" "$3" "$4" ;;
status) [ "${MOCK_READY:-1}" = 1 ] ;;
esac
'''
MOCK = '''#!/usr/bin/env python3
import os,pathlib,sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
root=pathlib.Path(os.environ['MOCK_ROOT'])
with open(root/'events','a') as log:log.write(name+' '+' '.join(args)+'\\n')
env=['SECRET_KEY=test','NODE_PORT=3000','NFTABLES_LOGGING=false','PATH=/usr/bin']
if name=='id':print('10001' if 'remnanode' in args else '0')
elif name in ('chown','getent','groupadd','useradd'):pass
elif name=='uname':print('Linux' if args==['-s'] else os.environ.get('MOCK_MACHINE','x86_64'))
elif name=='curl':
 url=next(a for a in args if a.startswith('https:'))
 asset=url.rsplit('/',1)[1]
 if asset=='checksums.txt' and '/geocheck/' in url:asset='geo-checksums.txt'
 source=root/'assets'/asset
 if not source.exists():sys.exit(22)
 pathlib.Path(args[args.index('-o')+1]).write_bytes(source.read_bytes())
elif name=='systemctl' or name=='rc-service':
 if args[-1]=='restart' or args[0]=='restart':
  if os.environ.get('MOCK_RESTART_FAIL')=='1':sys.exit(1)
 if args[0] in ('is-active','is-enabled') or args[-1]=='status':sys.exit(3)
elif name=='ss':
 # The port is held by a stranger (MOCK_PORT_BUSY=1) or by the node once restarted.
 started=any(line.split()[0] in ('systemctl','rc-service') and 'restart' in line.split() for line in open(root/'events'))
 if os.environ.get('MOCK_PORT_BUSY')=='1' or (started and os.environ.get('MOCK_LISTENS','1')=='1'):print('LISTEN 0 4096 *:2222 *:*')
elif name=='docker':
 docker=os.environ.get('MOCK_DOCKER','1')=='1'
 if args[0]=='info' and not docker:sys.exit(1)
 elif args[0]=='ps':print('old-node\\tremnawave/node:3.4.2')
 elif args[0]=='inspect':
  template=args[args.index('-f')+1]
  if 'len ' in template:print(len(env))
  elif '.Config.Env' in template:print(''.join(entry+'\\n' for entry in env))
  elif 'RestartPolicy' in template:print('unless-stopped 0')
  elif 'Running' in template:print('true')
'''


class ScriptTests(unittest.TestCase):
    def test_shell_syntax(self):
        subprocess.run(['sh', '-n', str(INSTALLER)], check=True)
        for path in sorted((ROOT/'scripts').glob('*.sh')):
            subprocess.run(['bash', '-n', str(path)], check=True)

    def test_portable_asn(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = pathlib.Path(directory)/'index.zip'
            subprocess.run(['python3',str(ROOT/'scripts/pack-asn.py'),str(archive)],input=b'{"asn":64500,"ipv4":["192.0.2.0/24"],"ipv6":["2001:db8::/32"]}\n',check=True)
            with zipfile.ZipFile(archive) as result:
                self.assertEqual(json.loads(result.read('64500.json'))['ipv4'], ['192.0.2.0/24'])

    def run_installer(self, root, runs=1, arch='amd64', manifest='checksums.txt', asn=True, corrupt=False, **environment):
        (root/'assets').mkdir()
        (root/'bin').mkdir()
        for name in ['id','uname','docker','curl','systemctl','rc-service','rc-update','ss','chown','getent','groupadd','useradd']:
            script=root/'bin'/name
            script.write_text(MOCK)
            script.chmod(0o755)
        assets=root/'assets'
        (assets/f'remnanode-linux-{arch}').write_bytes(NODE)
        if asn:
            with zipfile.ZipFile(assets/'asn-prefixes.zip','w') as archive:
                archive.writestr('64500.json','{"ipv4":[],"ipv6":[]}')
        with tarfile.open(assets/'geocheck_linux_amd64.tar.gz','w:gz') as archive:
            entry=tarfile.TarInfo('geocheck');entry.mode=0o755;content=b'#!/bin/sh\nexit 0\n';entry.size=len(content);archive.addfile(entry,io.BytesIO(content))
        checksums=''.join(hashlib.sha256(file.read_bytes()).hexdigest()+'  '+file.name+'\n' for file in sorted(assets.iterdir()))
        if corrupt:
            checksums=checksums.replace(hashlib.sha256(NODE).hexdigest(),'0'*64)
        (assets/manifest).write_text(checksums)
        (assets/'geo-checksums.txt').write_text(checksums)
        env=dict(os.environ,PATH=str(root/'bin')+':'+os.environ['PATH'],MOCK_ROOT=str(root),REMNANODE_INIT='systemd',
                 REMNANODE_SERVICE_INSTALLER=str(ROOT/'internal/serviceunit/install.sh'),
                 REMNANODE_INSTALL_DIR=str(root/'install'),REMNANODE_PREFIX=str(root/'prefix'),
                 REMNANODE_SYSTEMD_DIR=str(root/'units'),REMNANODE_INITD_DIR=str(root/'init.d'))
        env.pop('SECRET_KEY',None)
        env.pop('NODE_PORT',None)
        env.update(environment)
        for _ in range(runs):
            # A new session has no controlling terminal, so the installer cannot prompt.
            result=subprocess.run(['sh',str(INSTALLER)],env=env,capture_output=True,text=True,start_new_session=True)
            if result.returncode:
                break
        events=(root/'events').read_text() if (root/'events').exists() else ''
        return result, events

    def assert_rolled_back(self, root, result, events):
        self.assertNotEqual(result.returncode,0)
        self.assertIn('docker update --restart=unless-stopped old-node',events)
        self.assertIn('docker start old-node',events)
        self.assertNotIn('Node is ready',result.stdout)
        self.assertFalse((root/'install/remnanode').exists())
        self.assertFalse((root/'units/remnanode.service').exists())

    def test_migration_success_and_rerun(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,runs=2)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('Node is ready',result.stdout)
            settings=(root/'install/.env').read_text()
            self.assertIn('NFTABLES_LOGGING=false',settings)
            self.assertIn('SECRET_KEY=test',settings)
            self.assertNotIn('PATH=',settings)
            self.assertEqual((root/'install/.env').stat().st_mode&0o777,0o600)
            self.assertIn('ExecStart='+str(root/'install/remnanode'),(root/'units/remnanode.service').read_text())
            policy=(root/'units/remnanode.service.d/90-unprivileged.conf').read_text()
            for directive in ['User=remnanode','ProtectSystem=strict','ProtectHome=true','PrivateDevices=true','PrivateTmp=true','NoNewPrivileges=true','MemoryDenyWriteExecute=true','CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW']:
                self.assertIn(directive,policy)
            self.assertNotIn('User=root',(root/'units/remnanode.service').read_text())
            self.assertIn('User=remnanode',(root/'units/remnanode-update.service').read_text())
            self.assertIn('enable --now remnanode-restart.path remnanode-update.path',events)
            self.assertIn('chown -hR remnanode:remnanode',events)
            self.assertIn('docker stop -t 10 old-node',events)
            self.assertEqual(os.readlink(root/'prefix/bin/remnanode'),str(root/'install/remnanode'))
            self.assertTrue((root/'prefix/share/asn/asn-prefixes.zip').exists())

    def test_migration_readiness_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            self.assert_rolled_back(root,*self.run_installer(root,MOCK_READY='0'))

    def test_migration_restart_failure_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            self.assert_rolled_back(root,*self.run_installer(root,MOCK_RESTART_FAIL='1'))

    def test_fresh_install_waits_for_panel(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_READY='0',SECRET_KEY=' SECRET_KEY="abc+/=" ',NODE_PORT='2222')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('panel has not sent a configuration',result.stdout)
            self.assertEqual((root/'install/.env').read_text(),'SECRET_KEY=abc+/=\nNODE_PORT=2222\n')
            self.assertNotIn('docker stop',events)

    def test_fresh_install_requires_secret(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0')
            self.assertNotEqual(result.returncode,0)
            self.assertIn('SECRET_KEY is required',result.stderr)
            self.assertFalse((root/'install/remnanode').exists())
            self.assertNotIn('curl',events)

    def test_release_without_asn_and_with_sha256sums(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,_=self.run_installer(root,manifest='sha256sums.txt',asn=False)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('no ASN index',result.stdout)
            self.assertFalse((root/'prefix/share/asn/asn-prefixes.zip').exists())

    def test_checksum_mismatch_keeps_docker(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,corrupt=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('Checksum mismatch for remnanode-linux-amd64',result.stderr)
            self.assertNotIn('docker stop',events)
            self.assertFalse((root/'install/remnanode').exists())

    def test_mips_byte_order_detection(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            # The test host shell is a 64-bit little-endian ELF.
            result,events=self.run_installer(root,arch='mips64le',MOCK_MACHINE='mips64')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('remnanode-linux-mips64le',events)

    def test_arm_and_riscv_detection(self):
        for machine,arch in [('armv6l','armv6'),('armv5tel','armv5'),('armv7l','armv7'),('riscv64','riscv64')]:
            with self.subTest(machine=machine), tempfile.TemporaryDirectory() as directory:
                root=pathlib.Path(directory)
                result,events=self.run_installer(root,arch=arch,MOCK_MACHINE=machine)
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                self.assertIn('remnanode-linux-'+arch+'\n',events)

    def test_openrc_service(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,REMNANODE_INIT='openrc')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            script=(root/'init.d/remnanode').read_text()
            self.assertTrue(script.startswith('#!/sbin/openrc-run\n'))
            self.assertIn('command="'+str(root/'install/remnanode')+'"',script)
            self.assertEqual((root/'init.d/remnanode').stat().st_mode&0o777,0o755)
            self.assertIn('rc-update add remnanode default',events)
            self.assertIn('rc-service remnanode restart',events)


    def test_fresh_install_rejects_busy_port(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_PORT_BUSY='1',SECRET_KEY='abc',NODE_PORT='2222')
            self.assertNotEqual(result.returncode,0)
            self.assertIn('NODE_PORT 2222 is already in use',result.stdout+result.stderr)
            self.assertFalse((root/'install/remnanode').exists())
            self.assertFalse((root/'units/remnanode.service').exists())

    def test_fresh_install_reports_node_not_listening(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,_=self.run_installer(root,MOCK_DOCKER='0',MOCK_READY='0',MOCK_LISTENS='0',SECRET_KEY='abc',NODE_PORT='2222')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('not listening on port 2222',result.stdout)
            self.assertNotIn('panel has not sent a configuration',result.stdout)


if __name__=='__main__':unittest.main()
