"""Read-only HTTPS checks for the real Nginx used by disposable install tests."""
import subprocess


def verify_wallet_proxy(cert, *, legacy_redirect=False):
    # No session cookie: the API must return 401 without executing a mutation.
    # curl deliberately does not follow Location, unlike urllib's default opener.
    paths = [('GET', '/api/commerce/crypto/admin/wallets'),
             ('GET', '/api/commerce/crypto/admin/wallets?fixture=nginx'),
             ('POST', '/api/commerce/crypto/admin/wallets'),
             ('GET', '/api/commerce/crypto/admin/wallets/fixture/balances?chain_id=bsc'),
             ('POST', '/api/commerce/crypto/admin/wallets/restore')]
    for method, path in paths[:1] if legacy_redirect else paths:
        command = ['curl', '--silent', '--show-error', '--max-time', '10', '--noproxy', '*',
                   '--cacert', str(cert), '--resolve', 'panel.example.com:443:127.0.0.1',
                   '--request', method, '--header', 'X-Requested-With: guangyue',
                   '--dump-header', '-', '--output', '/dev/null']
        if method == 'POST':
            command += ['--header', 'Content-Type: application/json', '--data', '{}']
        result = subprocess.run(command + ['https://panel.example.com' + path], capture_output=True, timeout=15)
        if result.returncode:
            raise AssertionError('wallet proxy HTTPS request failed')
        headers = result.stdout.decode('ascii', errors='replace').splitlines()
        statuses = [int(line.split()[1]) for line in headers if line.startswith('HTTP/')]
        locations = [line for line in headers if line.lower().startswith('location:')]
        if legacy_redirect:
            assert statuses[-1:] == [301] and any(':10443/' in line for line in locations), 'legacy Nginx redirect was not reproduced'
        else:
            assert statuses[-1:] == [401] and not locations, 'wallet API must reach authentication without a redirect: ' + method + ' ' + path
