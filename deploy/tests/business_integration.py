#!/usr/bin/env python3
"""Real lightweight business-site installation on a disposable CI runner."""
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

if os.geteuid()!=0 or os.environ.get('GITHUB_ACTIONS')!='true':
    raise SystemExit('Only disposable GitHub Actions runners are supported.')
sys.dont_write_bytecode=True
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import install
import infrastructure
bundle=Path(sys.argv[1]).resolve()
base=Path(tempfile.mkdtemp(prefix='guangyue-business-ci-'));base.chmod(0o700)
children=[]
no_proxy=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def run(*args,success=True):
    result=subprocess.run(args,capture_output=True)
    if success and result.returncode:
        raise RuntimeError('test command failed: '+Path(args[0]).name+' exit='+str(result.returncode))
    return result

def api(path,data=None,method=None,cookie=''):
    headers={'X-Requested-With':'guangyue','Content-Type':'application/json'}
    if cookie:headers['Cookie']=cookie
    req=urllib.request.Request('http://127.0.0.1:19400'+path,headers=headers,data=json.dumps(data).encode() if data is not None else None,method=method)
    with no_proxy.open(req,timeout=30) as response:
        return json.load(response),response.headers.get('Set-Cookie','').split(';')[0]

def wait_for(check,seconds=100):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        try:
            result=check()
            if result:return result
        except (OSError,ValueError):pass
        time.sleep(1)
    raise RuntimeError('timed out waiting for business-site state')

def recv(sock,size):
    out=b''
    while len(out)<size:
        part=sock.recv(size-len(out))
        if not part:raise RuntimeError('SOCKS connection closed')
        out+=part
    return out

def udp_dns(port):
    with socket.create_connection(('127.0.0.1',port),5) as control:
        control.settimeout(15);control.sendall(b'\x05\x01\x00');assert recv(control,2)==b'\x05\x00'
        control.sendall(b'\x05\x03\x00\x01'+b'\x00'*6);head=recv(control,4);assert head[1]==0 and head[3]==1
        host=socket.inet_ntoa(recv(control,4));relay=struct.unpack('!H',recv(control,2))[0]
        if host=='0.0.0.0':host='127.0.0.1'
        query=b'\x38\x29\x01\x00\x00\x01'+b'\x00'*6+b'\x07example\x03com\x00\x00\x01\x00\x01'
        packet=b'\x00\x00\x00\x01'+socket.inet_aton('1.1.1.1')+struct.pack('!H',53)+query
        with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as udp:
            udp.settimeout(20);udp.sendto(packet,(host,relay));reply,_=udp.recvfrom(4096)
        answer=reply[10:];assert answer[:2]==query[:2] and answer[2]&128 and answer[3]&15==0 and struct.unpack('!H',answer[6:8])[0]>0

def start(args):
    log=(base/('process-'+str(len(children))+'.log')).open('wb')
    child=subprocess.Popen(args,stdout=log,stderr=log);children.append((child,log));return child

class TLSController(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def do_POST(self):
        size=int(self.headers.get('Content-Length','0'))
        if size>2<<20:self.send_error(413);return
        req=urllib.request.Request('http://127.0.0.1:19400'+self.path,data=self.rfile.read(size),headers={key:self.headers[key] for key in ('Authorization','X-Requested-With','Content-Type') if key in self.headers})
        try:
            with no_proxy.open(req,timeout=40) as response:
                data=response.read();self.send_response(response.status);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(data)
        except urllib.error.HTTPError as err:
            self.send_response(err.code);self.end_headers();self.wfile.write(err.read())

server=None
try:
    cert,key=base/'cert.pem',base/'key.pem'
    run('openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','3','-subj','/CN=controller.example.com','-addext','subjectAltName=DNS:controller.example.com,DNS:panel.example.com,DNS:node.example.com','-keyout',str(key),'-out',str(cert))
    shutil.copyfile(cert,'/usr/local/share/ca-certificates/guangyue-ci.crt');run('update-ca-certificates')
    # A test-only local address exercises production public-address validation.
    # The runner is disposable; no external host at this address is contacted.
    run('ip','address','add','8.0.0.1/32','dev','lo')
    with open('/etc/hosts','a') as out:out.write('\n8.0.0.1 controller.example.com\n')
    server=http.server.ThreadingHTTPServer(('8.0.0.1',19443),TLSController)
    tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(cert,key);server.socket=tls.wrap_socket(server.socket,server_side=True)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    infrastructure.provision()
    control=base/'controller';control.mkdir(mode=0o700);config=control/'config.json'
    binary=bundle/'bin/guangyue-linux-amd64';run(str(binary),'-config',str(config),'-init')
    data=json.loads(config.read_text());data.update(infrastructure.read_profile(infrastructure.PROFILE));data.update(edition='pro',role='controller',site_id='ci_controller',dev=True,state_dir=str(control/'state'),listen='127.0.0.1:19400',internal_listen='127.0.0.1:19401',public_url='https://controller.example.com:19443',web_dir=str(bundle/'web'))
    config.write_text(json.dumps(data));start([str(binary),'-config',str(config)])
    wait_for(lambda:api('/api/health'))
    initial=json.loads((control/'state/initial-owner.json').read_text());_,cookie=api('/api/login',initial)
    me,_=api('/api/state',cookie=cookie)
    created,_=api('/api/business-sites',{'id':'ci_edge','name':'CI edge','group':'Validation'},cookie=cookie)
    enrollment=base/'enrollment.json';enrollment.write_text(json.dumps(created['enrollment']));enrollment.chmod(0o600)
    policy={'name':'CI edge','group':'Validation','enabled':True,'exclusive':False,'revision':created['site']['revision'],'nodes':[],'grants':[{'user_id':me['me']['id'],'quota':0}]}
    api('/api/business-sites/ci_edge',policy,'PUT',cookie)
    command=[sys.executable,str(bundle/'deploy/install.py'),'--bundle',str(bundle),'--role','business','--enrollment-file',str(enrollment),'--panel-domain','panel.example.com','--node-domain','node.example.com','--cert',str(cert),'--key',str(key)]
    run(*command);assert not install.CONFIG.exists()
    run(*command,'--apply');install.health()
    actual=json.loads(install.CONFIG.read_text());assert actual['role']=='business' and actual['database']['driver']=='sqlite' and not actual['redis_url']
    assert not (install.STATE/'initial-owner.json').exists()
    assert 'MemoryMax=160M' in Path('/etc/systemd/system/guangyue.service').read_text()
    def synchronized():
        sites,_=api('/api/business-sites',cookie=cookie);site=sites['sites'][0]
        return site if site['applied'] and site['applied']==site['desired'] else None
    wait_for(synchronized)
    print('PASS Pro business installation: SQLite, low memory budget, HTTPS enrollment and acknowledged configuration',flush=True)
    sub,_=api('/api/subscription',cookie=cookie);nodes=[n for n in sub['nodes'] if n.get('site_id')=='ci_edge'];assert {n['protocol'] for n in nodes}=={'vless','hy2'}
    from urllib.parse import urlparse,parse_qs,unquote
    for node in nodes:
        uri=urlparse(node['uri']);q=parse_qs(uri.query);port=19891 if node['protocol']=='vless' else 19892
        if node['protocol']=='vless':
            cfg={'log':{'loglevel':'none'},'inbounds':[{'listen':'127.0.0.1','port':port,'protocol':'socks','settings':{'auth':'noauth','udp':True}}],'outbounds':[{'protocol':'vless','settings':{'vnext':[{'address':'127.0.0.1','port':443,'users':[{'id':uri.username,'encryption':'none','flow':'xtls-rprx-vision'}]}]},'streamSettings':{'network':'tcp','security':'reality','realitySettings':{'fingerprint':'chrome','serverName':q['sni'][0],'publicKey':q['pbk'][0],'shortId':q['sid'][0]}}}]}
            args=[str(bundle/'bin/xray-linux-amd64'),'run','-c']
        else:
            cfg={'server':'127.0.0.1:443','auth':unquote(uri.username),'tls':{'sni':q['sni'][0],'ca':str(cert)},'socks5':{'listen':'127.0.0.1:'+str(port)}}
            args=[str(bundle/'bin/hysteria-node-linux-amd64'),'client','--disable-update-check','-c']
        file=base/(node['protocol']+'.json');file.write_text(json.dumps(cfg));file.chmod(0o600);child=start(args+[str(file)])
        def tcp():
            result=run('curl','--noproxy','','--proxy','socks5h://127.0.0.1:'+str(port),'--max-time','20','-fsS','https://api.ipify.org',success=False)
            return result.returncode==0
        wait_for(tcp,70);udp_dns(port)
        print('PASS unified subscription '+node['protocol']+' real core TCP and UDP DNS',flush=True)
        child.terminate();child.wait(timeout=10)
    run(sys.executable,str(bundle/'deploy/upgrade.py'),'--bundle',str(bundle),'--apply');install.health();wait_for(synchronized)
    assert json.loads(install.CONFIG.read_text())['role']=='business'
    bad=base/'bad-bundle';shutil.copytree(bundle,bad);(bad/'bin/guangyue-linux-amd64').write_text('#!/bin/sh\nexit 42\n')
    rows=[hashlib.sha256((bad/name).read_bytes()).hexdigest()+'  '+name for _,name in (row.split('  ',1) for row in (bad/'SHA256SUMS').read_text().splitlines())];(bad/'SHA256SUMS').write_text('\n'.join(rows)+'\n')
    assert run(sys.executable,str(bundle/'deploy/upgrade.py'),'--bundle',str(bad),'--apply',success=False).returncode!=0
    install.health();wait_for(synchronized)
    restored,_=api('/api/subscription',cookie=cookie);assert sorted(n['uri'] for n in restored['nodes'] if n.get('site_id')=='ci_edge')==sorted(n['uri'] for n in nodes)
    print('PASS business upgrade and failed-executable rollback preserve scoped credentials and role',flush=True)
    site=synchronized();policy.update(revision=site['revision'],enabled=False,grants=[]);api('/api/business-sites/ci_edge',policy,'PUT',cookie)
    wait_for(lambda: not synchronized().get('sent_grants') if synchronized() else False)
    sub,_=api('/api/subscription',cookie=cookie);assert not any(n.get('site_id')=='ci_edge' for n in sub['nodes'])
    api('/api/business-sites/ci_edge',{},'DELETE',cookie)
    print('PASS revocation acknowledged before deletion and unified subscription withdrawal',flush=True)
finally:
    subprocess.run(['systemctl','stop',*install.UNITS],capture_output=True)
    if server:server.shutdown();server.server_close()
    for child,log in children:
        if child.poll() is None:
            child.terminate()
            try:child.wait(timeout=10)
            except subprocess.TimeoutExpired:child.kill();child.wait()
        log.close()
    shutil.rmtree(base)
