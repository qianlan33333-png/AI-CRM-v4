#!/usr/bin/env python3
"""Invoke Payment's local abandonment contract, never Provider cancellation.
Requires an existing authenticated admin cookie jar, CSRF token file and
nonempty human evidence file. Saves the receipt privately. Does not retry.
"""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

p = argparse.ArgumentParser(description=__doc__)
for arg in ('origin', 'cookie-jar', 'csrf-file', 'evidence-file', 'receipt-file'):
    p.add_argument('--' + arg, required=True)
p.add_argument('--payment-id', required=True, type=int)
a = p.parse_args()
origin = urllib.parse.urlsplit(a.origin)
if origin.scheme != 'https' or not origin.netloc or origin.path or origin.query or origin.fragment or origin.username or a.payment_id < 1:
    p.error('Expected HTTPS origin and positive payment ID')
jar = http.cookiejar.MozillaCookieJar(a.cookie_jar)
jar.load(ignore_discard=True)
csrf = Path(a.csrf_file).read_text().strip()
evidence = Path(a.evidence_file).read_bytes()
if not csrf or not evidence:
    p.error('CSRF token and evidence must be nonempty')
body = json.dumps({'confirmed_no_debit': True, 'evidence_digest': hashlib.sha256(evidence).hexdigest()}).encode()
request = urllib.request.Request(a.origin + '/api/admin/wechat-pay/payments/' + str(a.payment_id) + '/abandon-checkout', data=body, headers={'Content-Type': 'application/json', 'Origin': a.origin, 'X-CSRF-Token': csrf})
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), NoRedirect())
try:
    with opener.open(request, timeout=30) as response:
        status, raw = response.status, response.read(65536)
except urllib.error.HTTPError as error:
    status, raw = error.code, error.read(65536)
except (urllib.error.URLError, TimeoutError):
    raise SystemExit('Transport outcome unknown. Read Owner state before further action; do not blindly retry.')
fd = os.open(a.receipt_file, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(fd, 'wb') as receipt:
    receipt.write(raw)
if status != 200:
    raise SystemExit('Owner rejected command; private receipt saved, HTTP ' + str(status))
result = json.loads(raw)
if result.get('checkout_abandoned') is not True or result.get('provider_outcome_changed') is not False:
    raise SystemExit('Unexpected receipt. Inspect private evidence before further actions.')
print('Local checkout abandoned. Provider outcome unchanged. No replacement payment created.')
