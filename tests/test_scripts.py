#!/usr/bin/env python3
"""Run installer scenarios using only a temporary filesystem and mocks."""
import hashlib
import fcntl
import json
import os
import pathlib
import pty
import subprocess
import tempfile
import termios
import time
import unittest
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
INSTALLER = ROOT/'install.sh'
NODE = b'''#!/bin/sh
set -e
case "$1" in
version) echo 'gowave-node vtest' ;;
core) printf '#!/bin/sh\\nexit 0\\n' > "$(dirname "$0")/rw-core"; chmod +x "$(dirname "$0")/rw-core" ;;
service) sh "$GOWAVE_NODE_SERVICE_INSTALLER" "$3" "$4" "" "${5:-systemd}" ;;
status)
    printf 'node status %s\\n' "$2" >> "$MOCK_ROOT/events"
    case "$2" in
        --wait-running) [ "${MOCK_RUNNING:-1}" = 1 ] ;;
        --wait-ready) [ "${MOCK_READY:-1}" = 1 ] ;;
        *) exit 1 ;;
    esac ;;
'')
    printf 'node daemon %s %s %s\\n' "$PWD" "$GOWAVE_NODE_RUNTIME_DIR" "$ASN_INDEX_PATH" >> "$MOCK_ROOT/events"
    # The first daemon run crashes so service supervision can be observed.
    [ "$(grep -c '^node daemon' "$MOCK_ROOT/events")" -gt 1 ] || exit 1
    exec sleep 60 ;;
esac
'''
MOCK = '''#!/usr/bin/env python3
import json,os,pathlib,subprocess,sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
root=pathlib.Path(os.environ['MOCK_ROOT'])
with open(root/'events','a') as log:log.write(name+' '+' '.join(args)+'\\n')
env=['SECRET_KEY=test','NODE_PORT=3000','NFTABLES_LOGGING=false','PATH=/usr/bin']
if name=='id':print('10001' if any(a in ('gowave-node','remnanode') for a in args) else '0')
elif name in ('chown','getent','groupadd','useradd','ujail'):pass
elif name=='cp':
 subprocess.run(['/bin/cp',*args],check=True)
 # Model rc.common without writing to the host's /etc. Preserve the original
 # generated script separately so assertions still check the real template.
 dest=pathlib.Path(args[-1])
 if dest.parent==root/'init.d' and dest.name=='gowave-node.new':
  text=dest.read_text()
  if text.startswith('#!/bin/sh /etc/rc.common'):
   (root/'procd-template').write_text(text)
   dest.write_text(text.replace('/etc/rc.common',str(root/'rc.common'),1))
elif name=='uname':print('Linux' if args==['-s'] else os.environ.get('MOCK_MACHINE','x86_64'))
elif name=='curl':
 url=next(a for a in args if a.startswith('https:'))
 asset=url.rsplit('/',1)[1]
 source=root/'assets'/asset
 if not source.exists():sys.exit(22)
 pathlib.Path(args[args.index('-o')+1]).write_bytes(source.read_bytes())
elif name in ('systemctl','rc-service','rc-update','legacy-procd'):
 state_file=root/'service-state'
 state=json.loads(state_file.read_text()) if state_file.exists() else {}
 def initial(service):
  key='MOCK_LEGACY_ACTIVE' if service=='remnanode' else 'MOCK_LEGACY_PATHS_ACTIVE' if service.endswith('.path') and service.startswith('remnanode-') else 'MOCK_LEGACY_UPDATE_ACTIVE' if service=='remnanode-update.service' else 'MOCK_NATIVE_ACTIVE' if service=='gowave-node' else ''
  active=os.environ.get(key,'0')=='1'
  return {'active':active,'enabled':active}
 if name=='systemctl':
  action=args[0]; services=[a for a in args[1:] if not a.startswith('-')]
  if action=='show':
   if args[-1]=='remnanode' and os.environ.get('MOCK_LEGACY_DIR'):print(os.environ['MOCK_LEGACY_DIR'])
   if args[-1]=='gowave-node' and os.environ.get('MOCK_CURRENT_DIR'):print(os.environ['MOCK_CURRENT_DIR'])
   sys.exit(0)
 elif name=='rc-service':services=[args[0]];action=args[1]
 elif name=='legacy-procd':services=['remnanode'];action=args[0]
 elif args[0]=='show':
  for service in ('gowave-node','remnanode'):
   if state.get(service,initial(service))['enabled']:print(service+' | default')
  sys.exit(0)
 else:action='enable' if args[0]=='add' else 'disable';services=[args[1]]
 for service in services:
  current=state.setdefault(service,initial(service))
  if action in ('is-enabled','enabled'):
   if current['enabled']:print('enabled')
   else:sys.exit(3)
  if action in ('is-active','status','running'):sys.exit(0 if current['active'] else 3)
  if action=='restart' and service=='gowave-node' and os.environ.get('MOCK_RESTART_FAIL')=='1':sys.exit(1)
  if action in ('enable','disable'):current['enabled']=action=='enable'
  if action in ('start','stop','restart') or '--now' in args:current['active']=action in ('start','restart','enable')
 state_file.write_text(json.dumps(state))
elif name=='ss':
 # The port is held by a stranger (MOCK_PORT_BUSY=1) or by the node once restarted.
 started=any(line.split()[0] in ('systemctl','rc-service','procd') and 'restart' in line.split() for line in open(root/'events'))
 if os.environ.get('MOCK_PORT_BUSY')=='1' or (started and os.environ.get('MOCK_LISTENS','1')=='1'):print('LISTEN 0 4096 *:2222 *:*')
elif name=='docker':
 docker=os.environ.get('MOCK_DOCKER','1')=='1'
 if args[0]=='info' and not docker:sys.exit(1)
 elif args[0]=='ps':print('old-node\\t'+os.environ.get('MOCK_DOCKER_IMAGE','remnawave/node:3.4.2'))
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

    def run_installer(self, root, runs=1, arch='amd64', manifest='checksums.txt', asn=True, corrupt=False, terminal=False, **environment):
        (root/'assets').mkdir()
        (root/'bin').mkdir()
        for name in ['id','uname','docker','curl','systemctl','rc-service','rc-update','ss','chown','getent','groupadd','useradd','cp','ujail','legacy-procd','logger']:
            script=root/'bin'/name
            script.write_text(MOCK)
            script.chmod(0o755)
        (root/'rc.common').write_text("""#!/bin/sh
script=$1
action=$2
. "$script"
procd_open_instance() { :; }
procd_close_instance() { :; }
procd_set_param() { printf 'procd_param %s\\n' "$*" >> "$MOCK_ROOT/events"; }
case $action in
    enabled|running) [ "${MOCK_NATIVE_ACTIVE:-0}" = 1 ] ;;
    restart)
        printf 'procd restart\\n' >> "$MOCK_ROOT/events"
        [ "${MOCK_RESTART_FAIL:-0}" = 0 ] || exit 1
        start_service ;;
    enable|disable|stop) : ;;
esac
""")
        assets=root/'assets'
        (assets/f'gowave-node-linux-{arch}').write_bytes(NODE)
        if asn:
            with zipfile.ZipFile(assets/'asn-prefixes.zip','w') as archive:
                archive.writestr('64500.json','{"ipv4":[],"ipv6":[]}')
        checksums=''.join(hashlib.sha256(file.read_bytes()).hexdigest()+'  '+file.name+'\n' for file in sorted(assets.iterdir()))
        if corrupt:
            checksums=checksums.replace(hashlib.sha256(NODE).hexdigest(),'0'*64)
        (assets/manifest).write_text(checksums)
        env=dict(os.environ,PATH=str(root/'bin')+':'+os.environ['PATH'],MOCK_ROOT=str(root),GOWAVE_NODE_INIT='systemd',
                 GOWAVE_NODE_INSTALL_DIR=str(root/'install'),GOWAVE_NODE_PREFIX=str(root/'prefix'),
                 GOWAVE_NODE_SYSTEMD_DIR=str(root/'units'),GOWAVE_NODE_INITD_DIR=str(root/'init.d'),
                 GOWAVE_NODE_CONFIG_DIR=str(root/'config'),GOWAVE_NODE_RUNTIME_DIR=str(root/'run'))
        env.pop('SECRET_KEY',None)
        env.pop('NODE_PORT',None)
        env.update(environment)
        env={key:value for key,value in env.items() if value is not None}
        for _ in range(runs):
            if terminal:
                master, slave = pty.openpty()
                def attach_terminal():
                    os.setsid()
                    fcntl.ioctl(0, termios.TIOCSCTTY, 0)
                try:
                    result=subprocess.run(['sh',str(INSTALLER)],env=env,stdin=slave,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,preexec_fn=attach_terminal,timeout=20)
                finally:
                    os.close(master)
                    os.close(slave)
            else:
                # A new session has no controlling terminal, so the installer cannot prompt.
                result=subprocess.run(['sh',str(INSTALLER)],env=env,capture_output=True,text=True,start_new_session=True,timeout=20)
            if result.returncode:
                break
        events=(root/'events').read_text() if (root/'events').exists() else ''
        return result, events

    def assert_rolled_back(self, root, result, events):
        self.assertNotEqual(result.returncode,0)
        self.assertIn('docker update --restart=unless-stopped old-node',events)
        self.assertIn('docker start old-node',events)
        self.assertNotIn('Node is ready',result.stdout)
        self.assertFalse((root/'install/gowave-node').exists())
        self.assertFalse((root/'units/gowave-node.service').exists())
        self.assertFalse((root/'units/gowave-node.service.d/90-unprivileged.conf').exists())

    def prepare_previous_native(self, root, manager='systemd'):
        directory=root/'legacy';directory.mkdir()
        (directory/'remnanode').write_bytes(b'#!/bin/sh\nexit 0\n')
        (directory/'remnanode').chmod(0o755)
        for filename in ('rw-core','rw-core.stock','geoip.dat','geosite.dat','private-key.pem'):
            (directory/filename).write_bytes(('previous '+filename).encode())
        (directory/'rw-core').chmod(0o755)
        (directory/'private-key.pem').chmod(0o600)
        settings='SECRET_KEY=previous-secret\nNODE_PORT=2222\nREMNANODE_RUNTIME_DIR='+str(root/'custom-run')+'\nNFTABLES_LOGGING=false\n'
        (directory/'.env').write_text(settings)
        (directory/'.env').chmod(0o640)
        if manager=='systemd':
            units=root/'units';units.mkdir()
            (units/'remnanode.service').write_text('[Service]\nUser=remnanode\nWorkingDirectory='+str(directory)+'\n')
            for unit in ('remnanode-restart.path','remnanode-update.path'):
                (units/unit).write_text('# previous watcher\n')
        else:
            units=root/'init.d';units.mkdir()
            path=units/'remnanode'
            path.write_text('#!/bin/sh\nexec "$MOCK_ROOT/bin/legacy-procd" "$@"\n')
            path.chmod(0o755)
        return directory,settings

    def test_previous_native_migrates_in_place_and_reruns(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,_=self.prepare_previous_native(root)
            files={p.name:p.read_bytes() for p in previous.iterdir() if p.name!='.env'}
            result,events=self.run_installer(root,runs=2,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',MOCK_LEGACY_PATHS_ACTIVE='1',MOCK_PORT_BUSY='1',
                GOWAVE_NODE_INSTALL_DIR=None,GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous),GOWAVE_NODE_RUNTIME_DIR=None)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            for name,data in files.items():self.assertEqual((previous/name).read_bytes(),data)
            settings=(previous/'.env').read_text()
            self.assertIn('SECRET_KEY=previous-secret',settings)
            self.assertIn('NODE_PORT=2222',settings)
            self.assertIn('GOWAVE_NODE_RUNTIME_DIR='+str(root/'custom-run'),settings)
            self.assertNotIn('GOWAVE_NODE_SERVICE_USER',settings)
            self.assertNotIn('REMNANODE_',settings)
            self.assertFalse((root/'units/gowave-node.service.d/90-unprivileged.conf').exists())
            self.assertEqual(events.count('systemctl stop remnanode\n'),1)
            self.assertLess(events.index('/gowave-node-linux-amd64'),events.index('systemctl stop remnanode'))
            self.assertLess(events.index('systemctl stop remnanode'),events.index('systemctl restart gowave-node'))
            for name in ('remnanode','remnanode-go','gowave-node'):
                self.assertEqual(os.readlink(root/'prefix/bin'/name),str(previous/'gowave-node'))
            state=json.loads((root/'service-state').read_text())
            self.assertEqual(state['remnanode'],{'active':False,'enabled':False})
            self.assertTrue(state['gowave-node']['active'])

    def test_previous_native_failure_restores_service_and_environment(self):
        for failure in ({'MOCK_READY':'0'},{'MOCK_RESTART_FAIL':'1'}):
            with self.subTest(failure=failure),tempfile.TemporaryDirectory() as directory:
                root=pathlib.Path(directory)
                previous,settings=self.prepare_previous_native(root)
                before=(previous/'.env').stat()
                result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',
                    GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous),**failure)
                self.assertNotEqual(result.returncode,0)
                self.assertEqual((previous/'.env').read_text(),settings)
                after=(previous/'.env').stat()
                self.assertEqual((after.st_uid,after.st_gid,after.st_mode),(before.st_uid,before.st_gid,before.st_mode))
                self.assertFalse((previous/'gowave-node').exists())
                self.assertFalse((root/'units/gowave-node.service').exists())
                self.assertIn('systemctl restart remnanode',events)
                self.assertLess(events.index('systemctl stop gowave-node'),events.index('systemctl restart remnanode'))
                state=json.loads((root/'service-state').read_text())
                self.assertEqual(state['remnanode'],{'active':True,'enabled':True})

    def test_previous_native_checksum_failure_keeps_old_service_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,settings=self.prepare_previous_native(root)
            result,events=self.run_installer(root,corrupt=True,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',
                GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous))
            self.assertNotEqual(result.returncode,0)
            self.assertEqual((previous/'.env').read_text(),settings)
            self.assertNotIn('systemctl stop remnanode',events)
            self.assertNotIn('systemctl disable remnanode',events)

    def test_previous_native_openrc_and_procd(self):
        for manager in ('openrc','procd'):
            with self.subTest(manager=manager),tempfile.TemporaryDirectory() as directory:
                root=pathlib.Path(directory)
                previous,_=self.prepare_previous_native(root,manager)
                result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',GOWAVE_NODE_INIT=manager,
                    GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous))
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                command='rc-service remnanode stop' if manager=='openrc' else 'legacy-procd stop'
                self.assertIn(command,events)
                self.assertIn('node status --wait-ready',events)
                self.assertNotIn('systemctl daemon-reload',events)

    def test_previous_installer_options_remain_supported(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,_=self.prepare_previous_native(root)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',GOWAVE_NODE_INIT=None,GOWAVE_NODE_SYSTEMD_DIR=None,
                GOWAVE_NODE_INSTALL_DIR=None,GOWAVE_NODE_RUNTIME_DIR=None,GOWAVE_NODE_PREFIX=None,
                REMNANODE_INIT='systemd',REMNANODE_SYSTEMD_DIR=str(root/'units'),REMNANODE_INSTALL_DIR=str(previous),
                REMNANODE_RUNTIME_DIR=str(root/'custom-run'),REMNANODE_PREFIX=str(root/'prefix'),REMNANODE_VERSION='v2.0.0')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('/releases/download/v2.0.0/gowave-node-linux-amd64',events)
            self.assertNotIn('/releases/latest/',events)

    def test_previous_native_rejects_conflicting_live_services(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,settings=self.prepare_previous_native(root)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',MOCK_NATIVE_ACTIVE='1',
                GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous))
            self.assertNotEqual(result.returncode,0)
            self.assertIn('Both native services',result.stdout+result.stderr)
            self.assertEqual((previous/'.env').read_text(),settings)
            self.assertNotIn('curl ',events)

    def test_previous_native_discovers_systemd_working_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,_=self.prepare_previous_native(root)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',MOCK_LEGACY_DIR=str(previous),GOWAVE_NODE_INSTALL_DIR=None)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('systemctl show -p WorkingDirectory --value remnanode',events)
            self.assertTrue((previous/'gowave-node').exists())

    def test_previous_native_keeps_stopped_service_stopped_on_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,settings=self.prepare_previous_native(root)
            (root/'service-state').write_text(json.dumps({'remnanode':{'active':False,'enabled':True}}))
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_RUNNING='0',
                GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous))
            self.assertNotEqual(result.returncode,0)
            self.assertEqual((previous/'.env').read_text(),settings)
            self.assertNotIn('systemctl restart remnanode',events)
            self.assertEqual(json.loads((root/'service-state').read_text())['remnanode'],{'active':False,'enabled':True})

    def test_previous_native_imports_external_env_and_prefers_new_options(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,settings=self.prepare_previous_native(root)
            config=root/'previous-config';config.mkdir()
            (previous/'.env').rename(config/'.env')
            with (config/'.env').open('a') as stream:
                stream.write('GOWAVE_NODE_RUNTIME_DIR='+str(root/'preferred-run')+'\n')
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_LEGACY_ACTIVE='1',
                GOWAVE_NODE_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_INSTALL_DIR=str(previous),GOWAVE_NODE_LEGACY_CONFIG_DIR=str(config),
                GOWAVE_NODE_RUNTIME_DIR=None,GOWAVE_NODE_VERSION='v2.1.0',REMNANODE_VERSION='v2.0.0')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            current=(previous/'.env').read_text()
            self.assertIn('SECRET_KEY=previous-secret',current)
            self.assertIn('GOWAVE_NODE_RUNTIME_DIR='+str(root/'preferred-run'),current)
            self.assertIn('/releases/download/v2.1.0/',events)
            self.assertNotIn('/releases/download/v2.0.0/',events)
            self.assertTrue((config/'.env').read_text().startswith(settings))

    def test_existing_new_installation_wins_over_retired_old_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            previous,settings=self.prepare_previous_native(root)
            current=root/'install';current.mkdir()
            (current/'.env').write_text('SECRET_KEY=current-secret\nNODE_PORT=3333\n')
            (current/'gowave-node').write_bytes(NODE);(current/'gowave-node').chmod(0o755)
            (root/'units/gowave-node.service').write_text('[Service]\nWorkingDirectory='+str(current)+'\n')
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_NATIVE_ACTIVE='1',MOCK_CURRENT_DIR=str(current),
                MOCK_LEGACY_DIR=str(previous),GOWAVE_NODE_INSTALL_DIR=None)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('SECRET_KEY=current-secret',(current/'.env').read_text())
            self.assertEqual((previous/'.env').read_text(),settings)
            self.assertNotIn('systemctl stop remnanode',events)

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
            self.assertIn('ExecStart='+str(root/'install/gowave-node'),(root/'units/gowave-node.service').read_text())
            # The node runs as root without a sandbox: Xray must reach sockets and
            # files of other services (for example /dev/shm for nginx).
            self.assertIn('User=root',(root/'units/gowave-node.service').read_text())
            self.assertFalse((root/'units/gowave-node.service.d').exists())
            self.assertIn('docker stop -t 10 old-node',events)
            self.assertEqual(os.readlink(root/'prefix/bin/gowave-node'),str(root/'install/gowave-node'))
            self.assertTrue((root/'prefix/share/asn/asn-prefixes.zip').exists())

    def test_migration_readiness_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            self.assert_rolled_back(root,*self.run_installer(root,MOCK_READY='0'))

    def test_migration_from_gowave_node_image(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER_IMAGE='ghcr.io/aaaSaZaN/gowave-node:latest')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('docker stop -t 10 old-node',events)
            self.assertIn('node status --wait-ready',events)
            self.assertIn('https://github.com/aaaSaZaN/gowave-node/releases/latest/download/gowave-node-linux-amd64',events)

    def test_migration_restart_failure_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            self.assert_rolled_back(root,*self.run_installer(root,MOCK_RESTART_FAIL='1'))

    def test_fresh_install_finishes_without_panel_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_READY='0',SECRET_KEY=' SECRET_KEY="abc+/=" ',NODE_PORT='2222')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('Add the node in the panel with port 2222',result.stdout)
            self.assertIn('node status --wait-running',events)
            self.assertNotIn('node status --wait-ready',events)
            self.assertEqual((root/'install/.env').read_text(),'SECRET_KEY=abc+/=\nNODE_PORT=2222\n')
            self.assertNotIn('docker stop',events)

    def test_fresh_install_with_terminal_does_not_prompt_for_default_port(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,terminal=True,MOCK_DOCKER='0',MOCK_READY='0',SECRET_KEY='abc')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('NODE_PORT=3000',(root/'install/.env').read_text())
            self.assertIn('node status --wait-running',events)

    def test_fresh_install_requires_secret(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0')
            self.assertNotEqual(result.returncode,0)
            self.assertIn('SECRET_KEY is required',result.stderr)
            self.assertFalse((root/'install/gowave-node').exists())
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
            self.assertIn('Checksum mismatch for gowave-node-linux-amd64',result.stderr)
            self.assertNotIn('docker stop',events)
            self.assertFalse((root/'install/gowave-node').exists())

    def test_mips_byte_order_detection(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            # The test host shell is a 64-bit little-endian ELF.
            result,events=self.run_installer(root,arch='mips64le',MOCK_MACHINE='mips64')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertIn('gowave-node-linux-mips64le',events)

    def test_arm_and_riscv_detection(self):
        for machine,arch in [('armv6l','armv6'),('armv5tel','armv5'),('armv7l','armv7'),('riscv64','riscv64')]:
            with self.subTest(machine=machine), tempfile.TemporaryDirectory() as directory:
                root=pathlib.Path(directory)
                result,events=self.run_installer(root,arch=arch,MOCK_MACHINE=machine)
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                self.assertIn('gowave-node-linux-'+arch+'\n',events)

    def test_openrc_service(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,GOWAVE_NODE_INIT='openrc')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            script=(root/'init.d/gowave-node').read_text()
            self.assertTrue(script.startswith('#!/sbin/openrc-run\n'))
            self.assertIn('command="'+str(root/'install/gowave-node')+'"',script)
            self.assertEqual((root/'init.d/gowave-node').stat().st_mode&0o777,0o755)
            for unprivileged in ('command_user=','capabilities=','no_new_privs'):
                self.assertNotIn(unprivileged,script)
            self.assertNotIn('systemctl daemon-reload',events)
            self.assertFalse((root/'units/gowave-node.service.d').exists())
            self.assertIn('rc-update add gowave-node default',events)
            self.assertIn('rc-service gowave-node restart',events)

    def test_procd_service(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,GOWAVE_NODE_INIT='procd')
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            script=(root/'procd-template').read_text()
            self.assertTrue(script.startswith('#!/bin/sh /etc/rc.common\n'))
            self.assertIn('procd_set_param command "'+str(root/'install/gowave-node')+'"',script)
            self.assertNotIn('ujail',script)
            self.assertIn('procd restart',events)
            self.assertNotIn('systemctl daemon-reload',events)

    def test_entware_service_supervises_node(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            script=root/'init.d/S99gowave-node'
            control=lambda action:subprocess.run([str(script),action],capture_output=True,text=True,timeout=40)
            try:
                result,events=self.run_installer(root,GOWAVE_NODE_INIT='entware',MOCK_DOCKER='0',SECRET_KEY='abc')
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                text=script.read_text()
                self.assertTrue(text.startswith('#!/bin/sh\n'))
                self.assertEqual(script.stat().st_mode&0o777,0o755)
                self.assertIn('directory="'+str(root/'install')+'"',text)
                self.assertIn('GOWAVE_NODE_RUNTIME_DIR='+str(root/'run')+'\n',(root/'install/.env').read_text())
                self.assertNotIn('systemctl',events)
                for _ in range(100):
                    events=(root/'events').read_text()
                    if events.count('node daemon ')==2:break
                    time.sleep(0.1)
                self.assertEqual(events.count('node daemon '),2,events)
                self.assertIn('node daemon '+str(root/'install')+' '+str(root/'run')+' '+str(root/'prefix/share/asn/asn-prefixes.zip')+'\n',events)
                self.assertIn('logger -t gowave-node exited with status 1; restarting in 3 seconds',events)
                self.assertEqual(control('check').returncode,0)
                self.assertIn('already running',control('start').stdout)
                supervisor=int((root/'run.pid').read_text())
                stopped=control('stop')
                self.assertEqual(stopped.returncode,0,stopped.stdout+stopped.stderr)
                self.assertFalse((root/'run.pid').exists())
                self.assertFalse(pathlib.Path('/proc',str(supervisor)).exists())
                self.assertEqual(control('check').returncode,1)
                self.assertEqual((root/'events').read_text().count('node daemon '),2)
            finally:
                if script.exists():control('stop')

    def test_failed_native_update_preserves_environment_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            install=root/'install';install.mkdir()
            old_env=install/'.env';old_env.write_text('SECRET_KEY=previous-secret\nNODE_PORT=2222\n')
            old_env.chmod(0o640)
            if os.geteuid()==0:os.chown(old_env,65534,65534)
            before=old_env.stat()
            old_node=NODE+b'# previous native node\n'
            (install/'gowave-node').write_bytes(old_node);(install/'gowave-node').chmod(0o755)
            units=root/'units';units.mkdir()
            unit='[Service]\nUser=root\n'
            (units/'gowave-node.service').write_text(unit)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_NATIVE_ACTIVE='1',MOCK_READY='0')
            self.assertNotEqual(result.returncode,0)
            self.assertEqual(old_env.read_text(),'SECRET_KEY=previous-secret\nNODE_PORT=2222\n')
            after=old_env.stat()
            self.assertEqual((after.st_uid,after.st_gid,after.st_mode&0o777),(before.st_uid,before.st_gid,before.st_mode&0o777))
            self.assertEqual((install/'gowave-node').read_bytes(),old_node)
            self.assertEqual((units/'gowave-node.service').read_text(),unit)
            self.assertIn('cp -p '+str(old_env),events)
            self.assertEqual(events.count('systemctl restart gowave-node\n'),2)


    def test_fresh_install_rejects_busy_port(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_PORT_BUSY='1',SECRET_KEY='abc',NODE_PORT='2222')
            self.assertNotEqual(result.returncode,0)
            self.assertIn('NODE_PORT 2222 is already in use',result.stdout+result.stderr)
            self.assertFalse((root/'install/gowave-node').exists())
            self.assertFalse((root/'units/gowave-node.service').exists())

    def test_fresh_install_rolls_back_when_daemon_does_not_start(self):
        with tempfile.TemporaryDirectory() as directory:
            root=pathlib.Path(directory)
            result,events=self.run_installer(root,MOCK_DOCKER='0',MOCK_RUNNING='0',SECRET_KEY='abc',NODE_PORT='2222')
            self.assertNotEqual(result.returncode,0)
            self.assertIn('management daemon did not start',result.stdout+result.stderr)
            self.assertIn('systemctl stop gowave-node',events)
            self.assertFalse((root/'install/gowave-node').exists())
            self.assertFalse((root/'units/gowave-node.service').exists())


if __name__=='__main__':unittest.main()
