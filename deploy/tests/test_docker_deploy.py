import gzip
import hashlib
import json
import os
import socket
import stat
import subprocess
import sys
import tempfile
import unittest
import uuid
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import docker_deploy as docker
import oneclick


def plan():
    return {'schema':1,'id':str(uuid.uuid4()),'version':'0.41.0','edition':'lite','backend':'docker',
            'panel_domain':'panel.example.com','node_domain':'node.example.com','reality_sni':'www.cloudflare.com',
            'site_id':'default','acme_agreed':True,'created_at':1}


def images(): return {'app':'sha256:'+'a'*64,'postgres':'sha256:'+'b'*64,'redis':'sha256:'+'c'*64}


def manifest(version='0.41.0',digest='d'*64):
    return {'schema':1,'version':version,'platform':'linux/amd64','archive':f'guangyue-panel-docker-{version}-linux-amd64.tar.gz',
            'sha256':digest,'images':{key:{'id':value,**({'source':key+'@sha256:'+'e'*64} if key!='app' else {})} for key,value in images().items()}}


def marker(p=None):
    p=p or plan()
    return {**p,'private_gateway':True,'project':docker.project_name(p['id']),'compose_file':str(docker.ROOT/'compose.json'),
            'compose_sha256':'f'*64,'uid':1042,'gid':2042,'images':images(),'floor':'0.41.0','installed_at':1,'public_udp_verified':False}


def container(m,service='gateway',identifier='1'*64):
    return {'Id':identifier,'Image':m['images']['app' if service in ('gateway','panel') else service],
            'Config':{'Labels':{'org.guangyue.install-id':m['id'],'com.docker.compose.project':m['project'],'com.docker.compose.service':service}},
            'HostConfig':{'PortBindings':{'443/tcp':[{'HostIp':'0.0.0.0','HostPort':'443'}],'443/udp':[{'HostIp':'::','HostPort':'443'}]} if service=='gateway' else {}},
            'State':{'Running':True},'NetworkSettings':{'Networks':{'default':{'IPAddress':'172.20.0.2'}}}}


class TemporaryDockerTest(unittest.TestCase):
    def setUp(self):
        directory=tempfile.TemporaryDirectory();self.addCleanup(directory.cleanup);self.root=Path(directory.name)
        for name,value in [('ROOT',self.root/'docker'),('APP',self.root/'app'),('STATE',self.root/'state'),('CONFIG',self.root/'config.json'),
                           ('MARKER',self.root/'deployment.json'),('MAINTENANCE',self.root/'maintenance'),('BACKUPS',self.root/'backups'),('HELPER',self.root/'helper'),
                           ('DAEMON_CONFIG',self.root/'daemon.json'),('POLICY',self.root/'policy-rc.d'),('SYSTEM_UNITS',self.root/'units')]:
            p=patch.object(docker,name,value);p.start();self.addCleanup(p.stop)
        p=patch.object(oneclick,'ROOT',self.root/'installer');p.start();self.addCleanup(p.stop)
        for d in (docker.ROOT,docker.APP,docker.STATE,docker.BACKUPS,oneclick.ROOT):d.mkdir(mode=0o700)
        real_fstat=os.fstat
        def root_stat(fd):
            info=real_fstat(fd)
            values={name:getattr(info,name) for name in dir(info) if name.startswith('st_')};values['st_uid']=0
            return SimpleNamespace(**values)
        p=patch.object(docker.os,'fstat',side_effect=root_stat);p.start();self.addCleanup(p.stop)
        p=patch.object(docker.os,'fchown');p.start();self.addCleanup(p.stop)
        p=patch.object(oneclick,'private_directory',side_effect=lambda p:Path(p).mkdir(mode=0o700,parents=True,exist_ok=True));p.start();self.addCleanup(p.stop)
        self.gate=SimpleNamespace(begin=Mock(),finish=Mock(),marker=Mock(return_value=None))
        p=patch.dict(sys.modules,{'maintenance_gate':self.gate});p.start();self.addCleanup(p.stop)

    def json(self,path,value):
        Path(path).write_text(json.dumps(value));Path(path).chmod(0o600)


class MarkerTests(TemporaryDockerTest):
    def test_missing_marker_is_false_and_unsafe_marker_never_falls_back(self):
        self.assertFalse(docker.installed())
        native=dict(plan(),backend='native')
        p=self.root/'target';self.json(p,native);docker.MARKER.symlink_to(p)
        with self.assertRaises(ValueError):docker.installed()
        docker.MARKER.unlink();self.json(docker.MARKER,native);self.assertFalse(docker.installed())
        docker.MARKER.chmod(0o644)
        with self.assertRaisesRegex(ValueError,'权限'):docker.installed()

    def test_marker_rejects_unknown_fields_paths_id_platform_and_uid(self):
        for key,value in [('compose_file','/tmp/foreign-compose.json'),('project','other'),('uid',0),('gid',True),('unknown','field')]:
            m=marker();m[key]=value
            with self.subTest(key=key),self.assertRaises(ValueError):docker.validate_marker(m)
        m=marker();m['images']['app']='docker.io/evil:latest'
        with self.assertRaises(ValueError):docker.validate_marker(m)

    def test_json_rejects_non_root_even_when_private(self):
        self.json(docker.MARKER,marker())
        real=os.stat(docker.MARKER)
        with patch.object(docker.os,'fstat',return_value=SimpleNamespace(st_mode=real.st_mode,st_uid=1000,st_size=real.st_size)):
            with self.assertRaisesRegex(ValueError,'所有者'):docker.installed()

    def test_owned_json_refuses_external_change_and_retains_write_intent(self):
        journal={'id':str(uuid.uuid4())}
        path=docker.ROOT/'owned.json'
        docker.owned_json(path,{'a':1},journal)
        docker.owned_json(path,{'a':2},journal)
        self.assertEqual(len(journal['file_hashes'][str(path)]),2)
        path.write_text('external')
        with self.assertRaisesRegex(ValueError,'外部修改'):docker.owned_json(path,{'a':3},journal)


class ComposeTests(TemporaryDockerTest):
    def test_only_fixed_public_443_internal_db_and_nonroot_dynamic_user(self):
        m=marker();m['edition']='pro'
        comp=docker.composition(m,{'database':{'driver':'postgres'}},True)
        self.assertEqual(set(comp['services']),{'gateway','panel','postgres','redis'})
        gw,panel=comp['services']['gateway'],comp['services']['panel']
        self.assertEqual(gw['user'],'1042:2042');self.assertEqual(panel['user'],'1042:2042')
        self.assertEqual([(p['published'],p['protocol']) for p in gw['ports']],[('443','tcp'),('443','udp')])
        self.assertTrue(all(p['host_ip']=='0.0.0.0' for p in gw['ports']))
        self.assertTrue(any(v['source']==str(docker.MAINTENANCE) and v['read_only'] for v in gw['volumes']))
        self.assertEqual(panel['network_mode'],'service:gateway');self.assertTrue(panel['init'])
        self.assertEqual(panel['entrypoint'],['/usr/local/bin/guangyue-entrypoint'])
        self.assertNotIn('ports',panel);self.assertNotIn('networks',panel)
        for name,service in comp['services'].items():
            self.assertEqual(service['cap_drop'],['ALL']);self.assertIn('no-new-privileges:true',service['security_opt'])
            self.assertEqual(service['labels']['org.guangyue.install-id'],m['id'])
            self.assertFalse(service.get('privileged',False));self.assertNotEqual(service.get('network_mode'),'host')
            self.assertFalse(any('docker.sock' in v['source'] for v in service.get('volumes',[])))
            if name in ('postgres','redis'):self.assertNotIn('ports',service)
        self.assertFalse(any(v['source']==str(docker.STATE) for v in gw['volumes']))
        self.assertTrue(any(v['source']==str(docker.STATE/'gateway') for v in gw['volumes']))
        self.assertTrue(any(v['source']==str(docker.STATE) for v in panel['volumes']))
        self.assertTrue(any(v['source']==str(docker.CONFIG) and v['read_only'] for v in panel['volumes']))
        self.assertTrue(any(v['source']==str(docker.MAINTENANCE) and v['read_only'] for v in panel['volumes']))
        self.assertTrue(any(s.startswith('/run/guangyue-runtime:') and 'uid=1042' in s for s in panel['tmpfs']))
        for service in (gw,panel):self.assertTrue(any(s.startswith('/var/lib/postgresql/data:') for s in service['tmpfs']))

    def test_activate_recreates_shared_namespace_panel_but_not_database(self):
        m=marker();m['edition']='pro';comp=docker.composition(m,{})
        with patch.object(docker,'compose_for') as compose,patch.object(docker,'health'):
            docker.activate(m,comp)
        invocations=[c.args[1:] for c in compose.call_args_list]
        self.assertEqual(invocations[0],('stop','panel'))
        self.assertIn('--force-recreate',invocations[1]);self.assertEqual(invocations[1][-1],'gateway')
        self.assertIn('--no-deps',invocations[-1]);self.assertEqual(invocations[-1][-1],'panel')
        self.assertFalse(any('postgres' in row or 'redis' in row for row in invocations))


class ManifestTests(TemporaryDockerTest):
    def test_manifest_rejects_platform_version_ids_unknown_and_foreign_sources(self):
        self.assertEqual(docker.validated_manifest(manifest(),'0.41.0')['platform'],'linux/amd64')
        for key,value in [('platform','linux/arm64'),('version','0.41.1'),('archive','../../evil.tar.gz'),('unknown','field')]:
            data=manifest();data[key]=value
            with self.subTest(key=key),self.assertRaises(ValueError):docker.validated_manifest(data,'0.41.0')
        data=manifest();data['images']['postgres']['source']='evil/postgres@sha256:'+'e'*64
        with self.assertRaises(ValueError):docker.validated_manifest(data,'0.41.0')

    def test_loaded_image_inspect_rejects_id_arch_os_and_app_version(self):
        good=[{'Id':images()['app'],'Os':'linux','Architecture':'amd64','Config':{'Labels':{'org.opencontainers.image.version':'0.41.0'}}}]
        for key,value in [('Id',images()['redis']),('Os','windows'),('Architecture','arm64'),('Config',{'Labels':{'org.opencontainers.image.version':'0.40.1'}})]:
            bad=dict(good[0]);bad[key]=value
            with patch.object(docker,'run',return_value=json.dumps([bad])),self.assertRaises(ValueError):docker.image_check(manifest())

    def test_test_assets_guard_and_expansion_limit_precede_docker_load(self):
        with self.assertRaisesRegex(ValueError,'受控 CI'):docker.fetch_images('0.41.0',docker.ROOT/'out',self.root)
        assets=self.root/'assets';assets.mkdir();archive=assets/manifest()['archive']
        with gzip.open(archive,'wb') as f:f.write(b'x'*4096)
        self.json(assets/'DOCKER_IMAGES.json',manifest(digest=oneclick.sha256(archive)))
        with patch.dict(os.environ,{'GITHUB_ACTIONS':'true','GY_DOCKER_INTEGRATION':'1'}),patch.object(docker,'MAX_EXPANDED',128),patch.object(docker,'run') as run:
            with self.assertRaisesRegex(ValueError,'解压资源'):docker.fetch_images('0.41.0',docker.ROOT/'expanded',assets)
            run.assert_not_called()

    def test_checksum_mismatch_never_loads_even_in_ci(self):
        assets=self.root/'assets';assets.mkdir();archive=assets/manifest()['archive']
        with gzip.open(archive,'wb') as f:f.write(b'fixture')
        self.json(assets/'DOCKER_IMAGES.json',manifest())
        with patch.dict(os.environ,{'GITHUB_ACTIONS':'true','GY_DOCKER_INTEGRATION':'1'}),patch.object(docker,'run') as run:
            with self.assertRaisesRegex(ValueError,'SHA256'):docker.fetch_images('0.41.0',docker.ROOT/'bad-hash',assets)
            run.assert_not_called()


class ResumeTests(TemporaryDockerTest):
    def journal(self,p,item):
        self.json(docker.journal_path(),{'id':p['id'],'version':p['version'],'project':docker.project_name(p['id']),'containers':{'gateway':item['Id']}})

    def test_foreign_and_stopped_443_are_rejected_own_recorded_gateway_allowed(self):
        p=plan();m=marker(p);item=container(m);self.journal(p,item)
        with patch.object(docker,'container_inspect',return_value=[item]):docker.resume_preflight(p)
        for change in ('foreign-label','unrecorded','stopped-foreign'):
            other=container(m,identifier='2'*64)
            if change!='unrecorded':other['Config']['Labels']['org.guangyue.install-id']=str(uuid.uuid4())
            if change=='stopped-foreign':other['State']['Running']=False
            with self.subTest(change=change),patch.object(docker,'container_inspect',return_value=[item,other]),self.assertRaises(ValueError):docker.resume_preflight(p)

    def test_foreign_project_cannot_be_adopted_by_plan_uuid(self):
        p=plan();m=marker(p);item=container(m);self.journal(p,item)
        different=dict(p,id=str(uuid.uuid4()))
        with self.assertRaisesRegex(ValueError,'不匹配'):docker.resume_preflight(different)

    def test_no_proxy_evidence_never_exempts_socket_listener(self):
        p=plan();m=marker(p);item=container(m);self.journal(p,item)
        with patch.object(docker,'container_inspect',return_value=[item]),patch.object(docker.shutil,'which',return_value='/usr/bin/ss'),patch.object(docker,'run',return_value='tcp LISTEN 0 100 0.0.0.0:443 0.0.0.0:* users:(("foreign",pid=999999,fd=3))'):
            self.assertEqual(docker.resume_binding_owners(p),set())


class EngineTests(TemporaryDockerTest):
    def test_disabled_daemon_firewall_and_symlink_fail_before_service_probe(self):
        self.json(docker.DAEMON_CONFIG,{'iptables':False,'private-secret':'not echoed'})
        with patch.object(docker,'run') as run,self.assertRaisesRegex(ValueError,'原生') as error:
            docker.inspect_daemon_policy()
        self.assertNotIn('not echoed',str(error.exception));run.assert_not_called()
        docker.DAEMON_CONFIG.unlink();target=self.root/'actual-daemon.json';self.json(target,{'iptables':True});docker.DAEMON_CONFIG.symlink_to(target)
        with patch.object(docker,'run') as run,self.assertRaisesRegex(ValueError,'链接'):docker.inspect_daemon_policy()
        run.assert_not_called()

    def test_missing_iproute2_is_installed_without_upgrading_existing_packages(self):
        journal={'id':str(uuid.uuid4())};commands=[]
        def run(*args,**kwargs):
            commands.append(args)
            return 'Inst iproute2 (6.1 official [amd64])\n' if '-s' in args else ''
        with patch.object(docker.shutil,'which',side_effect=[None,'/usr/sbin/ip']),patch.object(oneclick,'apt_locks_available'),patch.object(docker,'run',side_effect=run):
            docker.prepare_inspection_tools(journal)
        self.assertIn('--no-upgrade',commands[-1]);self.assertIn('iproute2',commands[-1])
        self.assertTrue(any('--package-policy-start' in arg for arg in commands[-1]));self.assertNotIn('package_policy',journal)
        self.assertFalse(docker.POLICY.exists())

    def test_missing_iproute2_dependency_upgrade_is_rejected_before_install(self):
        journal={'id':str(uuid.uuid4())}
        with patch.object(docker.shutil,'which',return_value=None),patch.object(oneclick,'apt_locks_available'),patch.object(docker,'run',side_effect=['','Inst libbpf1 [1.0] (1.1 official [amd64])\n']) as run:
            with self.assertRaisesRegex(ValueError,'升级或删除'):docker.prepare_inspection_tools(journal)
        self.assertEqual(run.call_count,2);self.assertFalse(docker.POLICY.exists())

    def test_remote_docker_context_environment_cannot_redirect_host_deployment(self):
        with patch.dict(os.environ,{'DOCKER_HOST':'tcp://remote.example:2375','DOCKER_CONTEXT':'foreign'}),patch.object(docker.subprocess,'run',return_value=subprocess.CompletedProcess([],0,'{}','')) as run:
            docker.run('docker','info')
        self.assertEqual(run.call_args.args[0][:3],('docker','--host','unix:///var/run/docker.sock'))
        self.assertNotIn('DOCKER_HOST',run.call_args.kwargs['env']);self.assertNotIn('DOCKER_CONTEXT',run.call_args.kwargs['env'])
    def test_existing_daemon_is_never_restarted_or_reconfigured(self):
        with patch.object(docker,'inspect_daemon_policy'),patch.object(docker.shutil,'which',return_value='/usr/bin/docker'),patch.object(docker,'run',side_effect=['{}','2.35.0']) as run:
            docker.prepare_engine({'paths':[]})
        self.assertEqual([c.args[:2] for c in run.call_args_list],[('docker','info'),('docker','compose')])

    def test_existing_unavailable_daemon_fails_without_apt_or_service_action(self):
        with patch.object(docker,'inspect_daemon_policy'),patch.object(docker.shutil,'which',return_value='/usr/bin/docker'),patch.object(docker,'run',side_effect=ValueError('not running')) as run:
            with self.assertRaises(ValueError):docker.prepare_engine({'paths':[]})
        self.assertEqual(run.call_count,1)


class MaintenanceTests(TemporaryDockerTest):
    def config(self):return {'state_dir':str(docker.STATE),'edition':'pro','site_id':'default','database':{'driver':'postgres','dsn':'private'}}

    def test_pg_scripts_are_stdin_and_connection_credentials_not_in_command(self):
        m=marker();m['edition']='pro';config=self.config()
        with patch.object(docker,'load',return_value=m),patch.object(docker,'pg_container',return_value='1'*64),patch.object(docker.subprocess,'run',return_value=subprocess.CompletedProcess([],0,b'ok',b'')) as run:
            result=docker.postgres_run(['psql','-XAt','-c','SELECT version,checksum FROM gy_default.schema_migrations'],config,capture_output=True,check=True,env={'PGPASSWORD':'hidden','PATH':'known'})
        self.assertEqual(result.stdout,b'ok');args,kwargs=run.call_args
        self.assertNotIn('-c',args[0]);self.assertNotIn('hidden',str(args));self.assertEqual(kwargs['env'],{'PATH':'known'})
        self.assertIn(b'SELECT version',kwargs['input']);self.assertTrue(kwargs['check'])
        with patch.object(docker,'load',return_value=m),self.assertRaises(ValueError):docker.postgres_run(['sh','-c','id'],config)

    def test_pg_archive_path_is_streamed_not_interpolated_or_copied(self):
        m=marker();m['edition']='pro';archive=self.root/'private.dump';archive.write_bytes(b'archive')
        process=Mock();process.__enter__=Mock(return_value=process);process.__exit__=Mock(return_value=False);process.communicate.return_value=(b'listing',b'');process.returncode=0
        with patch.object(docker,'load',return_value=m),patch.object(docker,'pg_container',return_value='1'*64),patch.object(docker.subprocess,'Popen',return_value=process) as popen:
            result=docker.postgres_run(['pg_restore','--list',str(archive)],self.config(),capture_output=True,check=True)
            args,kwargs=popen.call_args;self.assertNotIn(str(archive),args[0]);self.assertIsInstance(kwargs['stdin'].name,int);self.assertEqual(result.stdout,b'listing')

    def test_schema_sql_is_site_scoped_and_unknown_site_rejected(self):
        config=self.config()
        with patch.object(docker,'postgres_run',return_value=subprocess.CompletedProcess([],0,b'version|hash\n',b'')) as run:
            self.assertEqual(docker.schema(config),hashlib.sha256(b'version|hash\n').hexdigest())
        self.assertIn('gy_default.schema_migrations',run.call_args.args[0][-1])
        config['site_id']='evil;DROP SCHEMA public'
        with self.assertRaises(ValueError):docker.schema(config)

    def test_incompatible_rollback_cannot_stop_or_mutate_anything(self):
        m=marker();m['version']='0.41.2'
        with patch.object(docker,'load',return_value=m),patch.object(docker,'candidates',return_value=[{'version':'0.41.1','compatible':False}]),patch.object(docker,'compose_for') as compose:
            with self.assertRaisesRegex(ValueError,'兼容'):docker.rollback('0.41.1')
            compose.assert_not_called()
        with patch.object(docker,'load',return_value=m),self.assertRaisesRegex(ValueError,'基线'):docker.rollback('0.40.9')

    def test_existing_freeze_is_never_adopted_or_released_by_new_transaction(self):
        m=marker();self.gate.marker.return_value=object()
        with patch.object(docker,'preflight'),patch.object(docker,'load',return_value=m),patch.object(docker,'read_config',return_value=self.config()),patch.object(docker,'compose_for') as compose:
            with self.assertRaisesRegex(ValueError,'已有维护冻结'):docker.transaction(lambda m,c:None,'0.41.1',lambda s:None)
        self.gate.begin.assert_not_called();self.gate.finish.assert_not_called();compose.assert_not_called()
        self.assertFalse(docker.transaction_file().exists())

    def test_gate_acquisition_failure_retains_journal_without_start_stop_or_release(self):
        m=marker();self.gate.begin.side_effect=OSError('durability failure')
        with patch.object(docker,'preflight'),patch.object(docker,'load',return_value=m),patch.object(docker,'read_config',return_value=self.config()),patch.object(docker,'schema',return_value='signature'),patch.object(docker,'compose_for') as compose:
            with self.assertRaisesRegex(OSError,'durability failure'):docker.transaction(lambda m,c:None,'0.41.1',lambda s:None)
        compose.assert_not_called();self.gate.finish.assert_not_called()
        self.assertEqual(docker.safe_json(docker.transaction_file())['stage'],'stopping')

    def test_changed_running_image_and_duplicate_service_cannot_pass_identity_check(self):
        m=marker();own=container(m);foreign=container(m,'panel','2'*64);foreign['Image']='sha256:'+'d'*64
        with patch.object(docker,'container_inspect',return_value=[own,foreign]),self.assertRaisesRegex(ValueError,'镜像'):docker.checked_containers(m)
        with patch.object(docker,'container_inspect',return_value=[own,container(m,identifier='3'*64)]),self.assertRaisesRegex(ValueError,'重复'):docker.checked_containers(m)

    def test_recovery_failure_keeps_committed_gate_and_never_restores_snapshot(self):
        m=marker();m['version']='0.41.1'
        self.json(docker.MARKER,m);self.json(docker.transaction_file(),{'schema':1,'id':m['id'],'stage':'committed','target':'0.41.1'})
        with patch.object(docker,'load',return_value=m),patch.object(docker,'validate_runtime',side_effect=ValueError('helper mismatch')),patch.object(docker,'health') as health,patch.object(docker,'restore_snapshot') as restore:
            with self.assertRaisesRegex(ValueError,'helper mismatch'):docker.recover()
        restore.assert_not_called();health.assert_not_called();self.gate.finish.assert_not_called()
        self.assertEqual(docker.safe_json(docker.transaction_file())['stage'],'committed')

    def test_failed_upgrade_restores_stopped_snapshot_and_keeps_transaction_on_restore_fault(self):
        m=marker();config=self.config();backup=docker.BACKUPS/'docker-upgrade-fixture';backup.mkdir()
        with patch.object(docker,'preflight'),patch.object(docker,'load',return_value=m),patch.object(docker,'read_config',return_value=config),patch.object(docker,'schema',return_value='signature'),patch.object(docker,'compose_for'),patch.object(docker,'backup_current',return_value=backup),patch.object(docker,'restore_snapshot',return_value=m) as restore:
            with self.assertRaisesRegex(ValueError,'forced'):docker.transaction(lambda m,c:(_ for _ in ()).throw(ValueError('forced')), '0.41.1',lambda s:None)
            restore.assert_called_once_with(backup)
            self.assertIsNone(docker.safe_json(docker.transaction_file()))

    def test_recovery_requires_own_transaction_identity(self):
        self.json(docker.transaction_file(),{'schema':1,'id':str(uuid.uuid4()),'stage':'stopping','previous':'0.41.0'})
        self.json(docker.MARKER,marker())
        with patch.object(docker,'compose_for') as compose,self.assertRaisesRegex(ValueError,'不属于'):docker.recover()
        compose.assert_not_called()

    def test_half_switched_compose_and_marker_are_recoverable_but_external_edit_is_not(self):
        old=marker();backup=docker.BACKUPS/'docker-upgrade-recovery';backup.mkdir()
        old_comp=docker.composition(old,{})
        old_digest=hashlib.sha256((json.dumps(old_comp,ensure_ascii=False,indent=2)+'\n').encode()).hexdigest();old['compose_sha256']=old_digest
        new=dict(old,version='0.41.1',images={**old['images'],'app':'sha256:'+'d'*64})
        new_comp=docker.composition(new,{})
        new['compose_sha256']=hashlib.sha256((json.dumps(new_comp,ensure_ascii=False,indent=2)+'\n').encode()).hexdigest()
        docker.atomic_json(docker.ROOT/'compose.json',new_comp);docker.atomic_json(docker.MARKER,old)
        self.json(docker.transaction_file(),{'schema':1,'id':old['id'],'stage':'changing','backup':str(backup),'switch':{'marker':new,'compose_sha256':new['compose_sha256']}})
        with patch.object(docker,'verify_backup',return_value=old):
            saved,current=docker.recovery_marker(backup);self.assertEqual(saved,old);self.assertEqual(current,old)
            docker.atomic_json(docker.MARKER,new)
            saved,current=docker.recovery_marker(backup);self.assertEqual(current,new)
            docker.atomic_json(docker.ROOT/'compose.json',{'name':'external'})
            with self.assertRaisesRegex(ValueError,'事务之外'):docker.recovery_marker(backup)

    def test_restore_failure_keeps_complete_transaction_for_restart_recovery(self):
        m=marker();config=self.config();backup=docker.BACKUPS/'docker-upgrade-fault';backup.mkdir()
        with patch.object(docker,'preflight'),patch.object(docker,'load',return_value=m),patch.object(docker,'read_config',return_value=config),patch.object(docker,'schema',return_value='signature'),patch.object(docker,'compose_for'),patch.object(docker,'backup_current',return_value=backup),patch.object(docker,'restore_snapshot',side_effect=ValueError('restore fault')):
            with self.assertRaisesRegex(ValueError,'restore fault'):docker.transaction(lambda m,c:(_ for _ in ()).throw(ValueError('forced')), '0.41.1',lambda s:None)
        self.assertEqual(docker.safe_json(docker.transaction_file())['backup'],str(backup))

    def test_snapshot_stops_panel_before_gateway_and_backup_failure_restarts_in_order(self):
        m=marker();events=[]
        def compose(m,*args,**kwargs):events.append(args)
        def backup(m,c):
            self.assertEqual(events[:2],[('stop','panel'),('stop','gateway')]);raise ValueError('backup failed')
        with patch.object(docker,'preflight'),patch.object(docker,'load',return_value=m),patch.object(docker,'read_config',return_value=self.config()),patch.object(docker,'schema',return_value='signature'),patch.object(docker,'compose_for',side_effect=compose),patch.object(docker,'backup_current',side_effect=backup),patch.object(docker,'health'):
            with self.assertRaisesRegex(ValueError,'backup failed'):docker.transaction(lambda m,c:None,'0.41.1',lambda s:None)
        self.assertEqual(events[2][-1],'gateway');self.assertEqual(events[3][0],'exec');self.assertEqual(events[4][-1],'panel')
        self.assertIsNone(docker.safe_json(docker.transaction_file()))

    def test_normal_rollback_changes_only_code_and_keeps_wallet_data_and_config(self):
        current=marker();current['version']='0.41.2';old=dict(current,version='0.41.1',images={**current['images'],'app':'sha256:'+'d'*64})
        saved=docker.BACKUPS/'docker-upgrade-old';saved.mkdir();(saved/'app').mkdir()
        self.json(saved/'DOCKER_IMAGES.json',manifest('0.41.1'))
        docker.atomic_json(docker.ROOT/'compose.json',docker.composition(current,{}))
        (docker.STATE/'master.key').write_bytes(b'wallet seed retained');docker.CONFIG.write_bytes(b'private config retained')
        captured=[]
        def transaction(change,target,progress):change(current,self.config());return saved
        with patch.object(docker,'load',return_value=current),patch.object(docker,'candidates',return_value=[{'version':'0.41.1','compatible':True,'backup':str(saved)}]),patch.object(docker,'verify_backup',return_value=old),patch.object(docker,'transaction',side_effect=transaction),patch.object(docker,'activate',side_effect=lambda m,c,**kw:captured.append((m,c))):
            docker.rollback('0.41.1')
        self.assertEqual((docker.STATE/'master.key').read_bytes(),b'wallet seed retained');self.assertEqual(docker.CONFIG.read_bytes(),b'private config retained')
        self.assertEqual(captured[0][0]['images']['app'],old['images']['app'])

    def test_committed_recovery_never_restores_old_financial_snapshot(self):
        m=marker();m['version']='0.41.1'
        self.json(docker.MARKER,m)
        self.json(docker.transaction_file(),{'schema':1,'id':m['id'],'stage':'committed','target':'0.41.1','backup':str(docker.BACKUPS/'old')})
        with patch.object(docker,'load',return_value=m),patch.object(docker,'validate_runtime',return_value=self.config()) as validate,patch.object(docker,'schema'),patch.object(docker,'health') as health,patch.object(docker,'restore_snapshot') as restore:
            docker.recover()
        restore.assert_not_called();health.assert_called_once_with(m,'0.41.1');self.gate.finish.assert_called_once()
        validate.assert_called_once_with(m,require_helper=True)
        self.assertIsNone(docker.safe_json(docker.transaction_file()))

    def test_recovered_snapshot_is_committed_before_releasing_gate(self):
        old=marker();backup=docker.BACKUPS/'recovery';backup.mkdir()
        self.json(docker.MARKER,old);self.json(docker.transaction_file(),{'schema':1,'id':old['id'],'stage':'changing','target':'0.41.1','previous':old['version'],'backup':str(backup)})
        self.gate.marker.return_value=object();self.gate.finish.side_effect=OSError('crash after restored gate release')
        with patch.object(docker,'restore_snapshot',return_value=old) as restore:
            with self.assertRaises(OSError):docker.recover()
        pending=docker.safe_json(docker.transaction_file());self.assertEqual(pending['stage'],'committed');self.assertEqual(pending['target'],old['version'])
        self.gate.begin.assert_not_called();restore.assert_called_once_with(backup)
        self.gate.finish.side_effect=None
        with patch.object(docker,'load',return_value=old),patch.object(docker,'validate_runtime',return_value=self.config()),patch.object(docker,'schema'),patch.object(docker,'health'),patch.object(docker,'restore_snapshot') as restore:
            docker.recover()
        restore.assert_not_called();self.assertIsNone(docker.safe_json(docker.transaction_file()))

    def test_gate_release_fault_after_commit_keeps_target_without_old_restore(self):
        old=marker();new=dict(old,version='0.41.1');backup=docker.BACKUPS/'backup';backup.mkdir()
        docker.HELPER.mkdir(mode=0o700);(docker.HELPER/'VERSION').write_text('0.41.1')
        self.gate.finish.side_effect=OSError('fsync interrupted')
        with patch.object(docker,'preflight'),patch.object(docker,'load',side_effect=[old,new]),patch.object(docker,'validate_runtime',return_value=self.config()),patch.object(docker,'read_config',return_value=self.config()),patch.object(docker,'schema',return_value='signature'),patch.object(docker,'compose_for'),patch.object(docker,'backup_current',return_value=backup),patch.object(docker,'remember'),patch.object(docker,'health'),patch.object(docker,'restore_snapshot') as restore:
            with self.assertRaisesRegex(ValueError,'版本已提交'):docker.transaction(lambda m,c:None,'0.41.1',lambda s:None)
        restore.assert_not_called();self.assertEqual(docker.safe_json(docker.transaction_file())['stage'],'committed')



class HealthcheckTests(unittest.TestCase):
    def test_new_and_upgraded_compositions_probe_panel_and_gateway(self):
        comp = docker.composition(marker(), {})
        panel, gateway = comp['services']['panel']['healthcheck'], comp['services']['gateway']['healthcheck']
        self.assertEqual(panel['test'][:3], ['CMD', '/bin/bash', '-c'])
        self.assertIn('/dev/tcp/127.0.0.1/19100', panel['test'][3]); self.assertIn('GET /api/health', panel['test'][3])
        # Compose interpolates `$` even in JSON files; the probe must not need it.
        self.assertNotIn('$', json.dumps(comp['services']['panel']['healthcheck']))
        self.assertEqual(gateway['test'][1:], ['/opt/guangyue-personal/bin/guangyue-gateway', '-config', '/etc/guangyue-gateway.json', '-wait-ready', '5s'])
        legacy = json.loads(json.dumps(comp))
        for name in ('panel', 'gateway'): legacy['services'][name].pop('healthcheck')
        self.assertEqual(docker.with_healthchecks(legacy), comp)
        custom = json.loads(json.dumps(comp)); custom['services']['panel']['healthcheck'] = {'disable': True}
        self.assertEqual(docker.with_healthchecks(custom)['services']['panel']['healthcheck'], {'disable': True})

    @unittest.skipUnless(Path('/bin/bash').is_file(), 'bash unavailable')
    def test_panel_probe_accepts_200_and_rejects_other_statuses(self):
        import socket, threading
        script = docker.PANEL_HEALTHCHECK['test'][3]
        for status, expected in (('200 OK', 0), ('503 Service Unavailable', 1)):
            server = socket.socket(); server.bind(('127.0.0.1', 0)); server.listen(1)
            port = server.getsockname()[1]
            def serve():
                connection, _ = server.accept()
                with connection:
                    request = connection.recv(4096)
                    assert request.startswith(b'GET /api/health HTTP/1.0\r\n'), request
                    connection.sendall(('HTTP/1.1 %s\r\nContent-Length: 2\r\n\r\n{}' % status).encode())
            thread = threading.Thread(target=serve); thread.start()
            try:
                result = subprocess.run(['/bin/bash', '-c', script.replace('19100', str(port))], timeout=10)
            finally:
                thread.join(5); server.close()
            with self.subTest(status=status): self.assertEqual(result.returncode != 0, bool(expected))


if __name__=='__main__':unittest.main()
