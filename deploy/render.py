#!/usr/bin/env python3
"""Render portable Nginx fragments without reading or modifying host configuration."""
import argparse
import json
import re
from pathlib import Path


def domain(value):
    if len(value) > 63 or not re.fullmatch(r'[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+', value):
        raise argparse.ArgumentTypeError('use a lowercase ASCII FQDN of at most 63 characters')
    return value


def render(panel, node, sni, web_hosts=()):
    hosts = list(dict.fromkeys([domain(panel), domain(node), *(domain(x) for x in web_hosts)]))
    domain(sni)
    if sni in hosts:
        raise ValueError('Reality SNI must differ from every hosted website domain')
    entries = '\n'.join(f'        {h} 127.0.0.1:10443;' for h in hosts)
    stream = '''# Include ONCE at nginx.conf top level, outside http {}.
stream {
    map $ssl_preread_server_name $guangyue_backend {
        %s 127.0.0.1:18443;
%s
        "~^(?=.{1,63}$)(?<guangyue_reality_sni>[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+)$" unix:/run/guangyue-reality/$guangyue_reality_sni.sock;
        default 127.0.0.1:10443;
    }
    server {
        listen 0.0.0.0:443;
        listen [::]:443 ipv6only=on;
        ssl_preread on;
        proxy_protocol on;
        proxy_pass $guangyue_backend;
        proxy_connect_timeout 10s;
        proxy_timeout 1h;
        access_log off;
        error_log /dev/null;
    }
}
''' % (sni, entries)
    panel_conf = '''# Include inside http {} (Debian/Ubuntu sites-enabled).
server {
    listen 127.0.0.1:10443 ssl http2 proxy_protocol;
    server_name %s;
    ssl_certificate /etc/nginx/guangyue-tls/current/fullchain.pem;
    ssl_certificate_key /etc/nginx/guangyue-tls/current/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    server_tokens off;
    client_max_body_size 5m;
    access_log off;
    error_log /dev/null;
    set_real_ip_from 127.0.0.1;
    real_ip_header proxy_protocol;
    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Content-Type-Options nosniff always;
    add_header Referrer-Policy no-referrer always;
    add_header X-Frame-Options DENY always;
    location = /api/support/upload {
        client_max_body_size 3m;
        proxy_request_buffering off;
        proxy_pass http://127.0.0.1:19100;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 30s;
    }
    location / {
        proxy_pass http://127.0.0.1:19100;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 45s;
    }
}
server {
    listen 80;
    listen [::]:80;
    server_name %s;
    access_log off;
    error_log /dev/null;
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/html;
        default_type text/plain;
        try_files $uri =404;
    }
    location / { return 301 https://%s$request_uri; }
}
''' % (' '.join(dict.fromkeys([panel, node])), ' '.join(dict.fromkeys([panel, node])), panel)
    return {'nginx-stream.conf': stream, 'nginx-panel.conf': panel_conf}


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--panel-domain', required=True, type=domain)
    p.add_argument('--node-domain', required=True, type=domain)
    p.add_argument('--reality-sni', default='www.cloudflare.com', type=domain)
    p.add_argument('--web-domain', action='append', default=[], type=domain, help='existing HTTPS site to preserve; repeat for each site')
    p.add_argument('--output', type=Path, required=True)
    return p


if __name__ == '__main__':
    args = parser().parse_args()
    files = render(args.panel_domain, args.node_domain, args.reality_sni, args.web_domain)
    args.output.mkdir(parents=True, exist_ok=True)
    for name, content in files.items():
        (args.output / name).write_text(content)
    print('Rendered two Nginx fragments. No host configuration changed.')
