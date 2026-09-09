#!/usr/bin/env python3
"""Provision isolated local PostgreSQL and Redis services for Pro (Debian/Ubuntu)."""
import argparse
import json
import os
import re
import secrets
import shutil
import socket
from urllib.parse import urlparse, unquote, parse_qs
import subprocess
from pathlib import Path
from common import deployment_lock

PROFILE = Path('/etc/guangyue-infrastructure.json')
PG_PORT, REDIS_PORT = 25433, 26380

def run(args, data=None):
    return subprocess.run(args, input=data, text=True, capture_output=True, check=True).stdout

def read_profile(path):
    path=Path(path)
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise ValueError('infrastructure profile must be a private regular file (mode 600)')
    value=json.loads(path.read_text())
    if not isinstance(value,dict) or not isinstance(value.get('database'),dict) or value['database'].get('driver')!='postgres':
        raise ValueError('profile requires a PostgreSQL database object')
    if not value['database'].get('dsn') or not isinstance(value.get('redis_url'),str) or not value['redis_url'].startswith(('redis://','rediss://')):
        raise ValueError('profile requires PostgreSQL DSN and Redis URL')
    return {key:value[key] for key in ('database','redis_url')}

def edition_config(config, edition, site, profile=None):
    result=dict(config)
    if not re.fullmatch(r'[a-z][a-z0-9_]{0,39}',site):
        raise ValueError('site ID must be a lowercase identifier, at most 40 characters')
    if edition=='pro':
        if profile: result.update(read_profile(profile))
        if result.get('database',{}).get('driver')!='postgres' or not result.get('redis_url'):
            raise ValueError('Pro requires --infrastructure-file; provision services first')
    elif config.get('edition')=='pro':
        raise ValueError('automatic Pro to Lite downgrade is refused; use portable backup recovery')
    else:
        result.update(database={'driver':'sqlite'},redis_url='')
    result.update(edition=edition,site_id=site)
    return result

def service_text(path, edition):
    text=Path(path).read_text()
    if edition=='pro':
        text=text.replace('Description=Guangyue Panel control panel','Description=Guangyue Panel Pro control panel').replace('GOMEMLIMIT=48MiB','GOMEMLIMIT=192MiB').replace('MemoryHigh=96M','MemoryHigh=256M').replace('MemoryMax=160M','MemoryMax=384M').replace('TasksMax=64','TasksMax=128')
    return text

def postgres_env(config):
    u=urlparse(config['database']['dsn'])
    if u.scheme not in ('postgres','postgresql') or not u.hostname or not u.path.strip('/'):
        raise ValueError('PostgreSQL maintenance requires a postgres:// connection URI')
    env=os.environ.copy()
    env.update(PGHOST=u.hostname,PGPORT=str(u.port or 5432),PGDATABASE=unquote(u.path[1:]),PGUSER=unquote(u.username or ''),PGPASSWORD=unquote(u.password or ''))
    for key,values in parse_qs(u.query).items():
        if key in ('sslmode','sslrootcert','sslcert','sslkey','connect_timeout'):env['PG'+key.upper()]=values[-1]
    env.setdefault('PGCONNECT_TIMEOUT','10')
    return env

def dump_postgres(config, path):
    env=postgres_env(config)
    with Path(path).open('xb') as output:
        subprocess.run(['pg_dump','--format=custom','--schema=gy_'+config.get('site_id','default')],env=env,stdout=output,stderr=subprocess.PIPE,check=True)

def restore_postgres(config, path):
    env=postgres_env(config)
    subprocess.run(['pg_restore','--dbname='+env['PGDATABASE'],'--clean','--if-exists','--no-owner','--single-transaction','--exit-on-error',str(path)],env=env,capture_output=True,check=True)

def provision():
    if PROFILE.exists():
        read_profile(PROFILE)
        print('Existing private infrastructure profile retained. No credentials or services changed.')
        return
    for port in (PG_PORT,REDIS_PORT):
        with socket.socket() as sock:
            try:sock.bind(('127.0.0.1',port))
            except OSError:raise ValueError('reserved local infrastructure port is in use')
    unit=Path('/etc/systemd/system/guangyue-redis.service')
    conf=Path('/etc/guangyue-redis.conf')
    if unit.exists() or conf.exists():raise ValueError('partial infrastructure exists; inspect before retrying')
    run(['apt-get','update','-qq'])
    run(['apt-get','install','-y','postgresql','redis-server'])
    majors=sorted((int(p.name) for p in Path('/usr/lib/postgresql').iterdir() if p.name.isdigit()))
    if not majors or majors[-1]<14:raise ValueError('PostgreSQL 14 or newer is required')
    major=str(majors[-1])
    if Path('/etc/postgresql',major,'guangyue').exists():raise ValueError('named PostgreSQL cluster already exists')
    run(['pg_createcluster',major,'guangyue','--port',str(PG_PORT),'--start-conf=auto','--', '--auth-local=peer','--auth-host=scram-sha-256'])
    pgconf=Path('/etc/postgresql',major,'guangyue','conf.d','guangyue.conf')
    pgconf.write_text("listen_addresses = '127.0.0.1'\nshared_buffers = '128MB'\nmax_connections = 40\nwork_mem = '4MB'\nmaintenance_work_mem = '64MB'\npassword_encryption = 'scram-sha-256'\nlog_statement = 'none'\nlog_min_error_statement = 'panic'\n")
    shutil.chown(pgconf,user='root',group='postgres');pgconf.chmod(0o640)
    budget=Path('/etc/systemd/system')/('postgresql@'+major+'-guangyue.service.d')
    budget.mkdir(mode=0o755,exist_ok=True);budget.chmod(0o755)
    (budget/'guangyue.conf').write_text('[Service]\nMemoryHigh=512M\nMemoryMax=768M\nTasksMax=128\n')
    (budget/'guangyue.conf').chmod(0o644)
    run(['systemctl','daemon-reload'])
    run(['pg_ctlcluster',major,'guangyue','start'])
    password=secrets.token_urlsafe(36)
    sql="CREATE ROLE guangyue LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '"+password+"';\nCREATE DATABASE guangyue OWNER guangyue;\n"
    run(['runuser','-u','postgres','--','psql','-X','-v','ON_ERROR_STOP=1','-p',str(PG_PORT),'postgres'],sql)
    redis_password=secrets.token_urlsafe(36)
    conf.write_text(f'bind 127.0.0.1\nport {REDIS_PORT}\nprotected-mode yes\nrequirepass {redis_password}\nmaxmemory 64mb\nmaxmemory-policy allkeys-lru\nsave ""\nappendonly no\ndaemonize no\nloglevel warning\nlogfile /dev/null\n')
    shutil.chown(conf,user='root',group='redis');conf.chmod(0o640)
    unit.write_text('[Unit]\nDescription=Guangyue ephemeral Redis cache\nAfter=network.target\n[Service]\nUser=redis\nGroup=redis\nExecStart=/usr/bin/redis-server /etc/guangyue-redis.conf\nRestart=on-failure\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\nMemoryMax=128M\nTasksMax=32\nUMask=0077\n[Install]\nWantedBy=multi-user.target\n')
    unit.chmod(0o644)
    run(['systemctl','daemon-reload']);run(['systemctl','enable','--now','guangyue-redis'])
    run(['systemctl','enable','postgresql@'+major+'-guangyue'])
    value={'database':{'driver':'postgres','dsn':f'postgres://guangyue:{password}@127.0.0.1:{PG_PORT}/guangyue?sslmode=disable','max_connections':8},'redis_url':f'redis://:{redis_password}@127.0.0.1:{REDIS_PORT}/0'}
    with PROFILE.open('x') as output:json.dump(value,output,indent=2);output.write('\n')
    PROFILE.chmod(0o600)
    print('Pro infrastructure ready. Private profile: '+str(PROFILE))
    print('PostgreSQL: local port '+str(PG_PORT)+'; Redis: local port '+str(REDIS_PORT)+'.')

def main():
    os.umask(0o077)
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--apply',action='store_true');args=parser.parse_args()
    if os.geteuid()!=0:raise ValueError('run as root')
    if args.apply:
        with deployment_lock():provision()
    else:print('Creates a dedicated PostgreSQL cluster and Redis instance on loopback. Run with --apply to provision; existing services are preserved.')

if __name__=='__main__':
    try:main()
    except (ValueError,OSError,subprocess.CalledProcessError) as exc:
        raise SystemExit('Infrastructure setup failed: '+(str(exc) if isinstance(exc,ValueError) else type(exc).__name__)+'. Existing application was not changed; inspect any partial infrastructure before retrying.')
