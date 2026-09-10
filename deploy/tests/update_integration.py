#!/usr/bin/env python3
"""Actual socket/systemd update and data-preserving rollback on a disposable runner."""
import http.client
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
from pathlib import Path
if os.geteuid()!=0 or os.environ.get('GITHUB_ACTIONS')!='true':raise SystemExit('Disposable GitHub runner required')
sys.dont_write_bytecode=True
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import update_agent as agent
import update_state as state
import install
candidate=Path(sys.argv[1]).resolve();edition=os.environ.get('GY_INTEGRATION_EDITION','lite');target=(candidate/'VERSION').read_text().strip()
base=Path(tempfile.mkdtemp(prefix='guangyue-update-ci-',dir='/root'));base.chmod(0o700)
def run(*args):
 p=subprocess.run(args,capture_output=True)
 if p.returncode:raise RuntimeError('Failed executable '+Path(args[0]).name+' exit '+str(p.returncode))
 return p.stdout
class UnixHTTP(http.client.HTTPConnection):
 def connect(self):
  self.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);self.sock.settimeout(35);self.sock.connect(state.SOCKET)
def updater(path,body=None):
 c=UnixHTTP('localhost');c.request('GET' if body is None else 'POST',path,body=None if body is None else json.dumps(body),headers={'Content-Type':'application/json'});r=c.getresponse();data=json.load(r);c.close();return r.status,data
def api(path,body=None,cookie=''):
 req=urllib.request.Request('http://127.0.0.1:19100'+path,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json','X-Requested-With':'guangyue','Cookie':cookie})
 with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=35) as response:return json.load(response),response.headers.get('Set-Cookie','').split(';')[0]
def finished():
 for _ in range(120):
  code,data=updater('/state');op=data.get('operation') or {}
  if code!=200 or not op:
   time.sleep(1);continue
  if op.get('stage') not in agent.ACTIVE:
   assert op.get('stage')=='succeeded',op.get('error','no operation');return data
  time.sleep(1)
 raise RuntimeError('Updater timed out')
try:
 # Use an authentic previous release, never a binary with an edited version label.
 old=agent.fetch_bundle('0.16.0',edition,base)
 for name in ['xray','mihomo']:shutil.copyfile(candidate/'bin'/(name+'-linux-amd64'),old/'bin'/(name+'-linux-amd64'));(old/'bin'/(name+'-linux-amd64')).chmod(0o755)
 cert,key=base/'cert.pem',base/'key.pem'
 run('openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','3','-subj','/CN=panel.example.com','-addext','subjectAltName=DNS:panel.example.com,DNS:node.example.com','-keyout',str(key),'-out',str(cert))
 command=[sys.executable,str(old/'deploy/install.py'),'--bundle',str(old),'--panel-domain','panel.example.com','--node-domain','node.example.com','--cert',str(cert),'--key',str(key),'--apply']
 if edition=='pro':
  run(sys.executable,str(old/'deploy/infrastructure.py'),'--apply');command+=['--site-id','ci_update','--infrastructure-file','/etc/guangyue-infrastructure.json']
 run(*command);assert api('/api/health')[0]['version']=='0.16.0'
 owner=json.loads((install.STATE/'initial-owner.json').read_text());_,cookie=api('/api/login',owner);before,_=api('/api/subscription',cookie=cookie)
 state.setup('0.16.0',candidate/'deploy')
 # The not-yet-published candidate is injected only into this disposable runner's
 # downloader; socket permissions, API, manifests, real binaries and all mutations run normally.
 launcher=base/'fixture-updater.py'
 launcher.write_text("import sys,shutil\nfrom pathlib import Path\nsys.path.insert(0,'/opt/guangyue-updater')\nimport update_agent as agent\ndef candidate(version,edition,temp):\n p=temp/'candidate';shutil.copytree("+repr(str(candidate))+",p);return p\nagent.fetch_bundle=candidate\nagent.serve()\n")
 override=Path('/etc/systemd/system/guangyue-updater.service.d');override.mkdir()
 (override/'ci.conf').write_text('[Service]\nExecStart=\nExecStart=/usr/bin/python3 '+str(launcher)+'\n')
 run('systemctl','daemon-reload')
 state.write('catalog.json',{'checked_at':int(time.time()),'releases':[{'version':target,'editions':[edition],'name':'CI candidate','notes':'Synthetic release metadata','url':'https://github.com/'+state.REPO+'/releases/tag/v'+target,'published_at':''}]})
 assert Path(state.SOCKET).stat().st_mode&0o777==0o660
 code,initial=updater('/state');assert code==200 and initial['installed_version']=='0.16.0'
 request=dict(action='update',version=target,expected_version='0.16.0',request_id=str(uuid.uuid4()))
 code,op=updater('/apply',request);assert code==200
 code,replayed=updater('/apply',request);assert code==200 and replayed['request_id']==op['request_id']
 result=finished();assert result['current_version']==target and result['installed_version']=='0.16.0'
 assert api('/api/health')[0]['version']==target
 after,_=api('/api/subscription',cookie=cookie);assert sorted(n['uri'] for n in before['nodes'])==sorted(n['uri'] for n in after['nodes'])
 code,newstate=api('/api/updates',cookie=cookie);assert code['available']
 print('PASS '+edition+' socket-activated verified update, idempotency, target health and stable subscription',flush=True)
 # Create data AFTER the upgrade; rolling back must not restore the older database snapshot.
 new_user={'username':'after-update','password':'synthetic-after-update-password','enabled':True,'vless':True,'hy2':True,'expires':0,'quota':0}
 api('/api/users',new_user,cookie)
 state_before,_=api('/api/state',cookie=cookie);assert any(u['username']=='after-update' for u in state_before['users'])
 request=dict(action='rollback',version='0.16.0',expected_version=target,request_id=str(uuid.uuid4()))
 api('/api/updates/apply',request,cookie);result=finished()
 assert result['current_version']=='0.16.0' and result['installed_version']=='0.16.0'
 state_after,_=api('/api/state',cookie=cookie);assert any(u['username']=='after-update' for u in state_after['users'])
 after,_=api('/api/subscription',cookie=cookie);assert sorted(n['uri'] for n in before['nodes'])==sorted(n['uri'] for n in after['nodes'])
 print('PASS '+edition+' authenticated rollback preserves new users, subscription and installation floor',flush=True)
 forbidden=dict(action='rollback',version='0.15.0',expected_version='0.16.0',request_id=str(uuid.uuid4()))
 assert updater('/apply',forbidden)[0]==409;assert api('/api/health')[0]['version']=='0.16.0'
 print('PASS server rejects versions below the original installation even after rollback',flush=True)
 # Update once more through the retained helper, proving old UI rollback is recoverable.
 request=dict(action='update',version=target,expected_version='0.16.0',request_id=str(uuid.uuid4()))
 assert updater('/apply',request)[0]==200;finished()
 assert api('/api/health')[0]['version']==target
 print('PASS retained helper upgrades again after rollback to the older panel',flush=True)
finally:
 journal=subprocess.run(['journalctl','-u','guangyue-updater','--no-pager','-o','cat'],capture_output=True,text=True).stdout
 for line in journal.splitlines():
  if line.startswith('Update failure '):print(line,flush=True)
 subprocess.run(['systemctl','stop','guangyue-updater.socket','guangyue-updater.service',*install.UNITS],capture_output=True)
 shutil.rmtree(base)
