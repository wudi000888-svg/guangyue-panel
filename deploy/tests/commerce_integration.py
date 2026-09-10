"""Exercise real balance redemption, order provisioning and image workers in CI."""
import base64
import secrets
import subprocess
import time


def purchase_plan(call, member, password, plan, user_id, bundle):
    settings = call('/commerce/settings')
    call('/commerce/settings', {'settings': dict(settings, sales=True), 'password': password})
    generated = call('/commerce/codes', {'amount': '5000', 'count': 2, 'expires': int(time.time()) + 3600, 'note': 'CI fixture', 'password': password, 'operation_id': secrets.token_hex(16)})
    redemption = {'code': generated['codes'][0]['code'], 'operation_id': secrets.token_hex(16)}
    member('/commerce/redeem', redemption)
    member('/commerce/redeem', redemption)
    assert member('/commerce/wallet')['available'] == '5000', 'redemption replay duplicated funds'
    call('/commerce/codes/revoke', {'ids': [generated['codes'][1]['id']], 'password': password, 'operation_id': secrets.token_hex(16)})
    offer = call('/commerce/offers', {'plan_id': plan['id'], 'plan_version': plan['version'], 'enabled': True, 'price': '1000'})
    order = member('/commerce/orders', {'offer_id': offer['id'], 'offer_version': offer['version'], 'operation_id': secrets.token_hex(16)})
    assert order['id'].startswith('GYO-')
    member('/commerce/orders/action', {'id': order['id'], 'action': 'confirm', 'operation_id': secrets.token_hex(16)})
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        current = next(v for v in member('/commerce/orders')['items'] if v['id'] == order['id'])
        if current['state'] == 'completed': break
        time.sleep(1)
    assert current['state'] == 'completed', 'paid order did not provision'
    wallet = member('/commerce/wallet')
    assert wallet['available'] == '4000' and wallet['held'] == '0'
    # A tiny valid PNG passes through the installed binary's worker subprocess.
    # Generate a canonical PNG using Python's standard library, avoiding decoder-specific fixtures.
    import struct, zlib
    def chunk(kind, data): return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', zlib.crc32(kind + data))
    png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', 1, 1, 8, 6, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(b'\0\xff\0\0\xff')) + chunk(b'IEND', b'')
    worker = subprocess.run([str(bundle / 'bin/guangyue-linux-amd64'), '-normalize-ticket-image', 'png'], input=png, capture_output=True)
    assert worker.returncode == 0 and worker.stdout.startswith(b'\x89PNG'), 'installed image worker failed'
    image = member('/support/upload', {'name': 'screenshot.png', 'mime': 'image/png', 'data': base64.b64encode(png).decode()})
    ticket = member('/support/tickets', {'title': 'Order support', 'category': 'order', 'body': 'CI support fixture', 'order_id': order['id'], 'attachments': [image['id']], 'operation_id': secrets.token_hex(16)})
    call('/support/reply', {'id': ticket['id'], 'body': 'Internal CI note', 'internal': True, 'operation_id': secrets.token_hex(16)})
    if member('/state')['me']['role'] != 'owner':
        assert all(not row['internal'] for row in member('/support/ticket?id=' + ticket['id'])['replies']), 'private support note leaked'
    call('/commerce/settings', {'settings': settings, 'password': password})
    print('PASS installed redemption replay, code revocation, paid order, image subprocess and ticket ownership')
