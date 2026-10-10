"""Run exact candidate HTTP/auth methods with synthetic state and no service main.

SIDEBAR_CANDIDATE_API points to the staged candidate, never an active module.
The handler source is compiled without module startup/environment/provider code.
"""
import ast
import base64
import copy
import hashlib
import hmac
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from urllib.parse import parse_qs, urlparse
from unittest.mock import patch

BASE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(BASE))
sys.path.insert(0, str(BASE / 'ops'))
from build_candidate import patch_http
import sidebar_ai_reader
import sidebar_manual_generation
from test_manual_generation import fixture, request, Engine


SYNTHETIC_BASE = '''import os
AUTH_USER = os.environ.get('DARU_AUTH_USER', 'daru')
AUTH_PASSWORD = os.environ.get('DARU_AUTH_PASSWORD', '')
ADMIN_AUTH_USER = os.environ.get('DARU_ADMIN_USER', 'owner')
ADMIN_AUTH_PASSWORD = os.environ.get('DARU_ADMIN_PASSWORD', '')
ACCESS_TOKEN = AGENT_READ_TOKEN = AGENT_OPERATOR_TOKEN = ''
AUTH_ENABLED = bool(AUTH_PASSWORD or ADMIN_AUTH_PASSWORD or ACCESS_TOKEN or AGENT_READ_TOKEN or AGENT_OPERATOR_TOKEN)
AGENT_GET_PATHS = {'/api/local/agent/read'}
AGENT_POST_PATHS = {'/api/local/agent/manual'}
class Handler:
  def _send(self, status, payload):
    self.send_response(status)
    self.wfile.write(json.dumps(payload).encode())
  def _send_auth_required(self):
    self._send(401, {'error': 'authentication_required'})
  def _auth_identity(self):
    if not AUTH_ENABLED:
      return {'user': 'local', 'role': 'admin'}
    header = self.headers.get('Authorization', '')
    if header.startswith('Bearer '):
      token = header.removeprefix('Bearer ').strip()
      if AGENT_OPERATOR_TOKEN and hmac.compare_digest(token, AGENT_OPERATOR_TOKEN):
        return {'user': 'agent', 'role': 'agent_operator'}
      if AGENT_READ_TOKEN and hmac.compare_digest(token, AGENT_READ_TOKEN):
        return {'user': 'agent-read', 'role': 'agent_read'}
      if ACCESS_TOKEN and hmac.compare_digest(token, ACCESS_TOKEN):
        return {'user': 'token', 'role': 'admin'}
      return None
    if header.startswith('Basic '):
      try:
        raw = base64.b64decode(header.removeprefix('Basic ').strip()).decode('utf-8')
      except Exception:
        return None
      user, sep, password = raw.partition(':')
      if not sep:
        return None
      if ADMIN_AUTH_PASSWORD and hmac.compare_digest(user, ADMIN_AUTH_USER) and hmac.compare_digest(password, ADMIN_AUTH_PASSWORD):
        return {'user': user, 'role': 'admin'}
      if AUTH_PASSWORD and hmac.compare_digest(user, AUTH_USER) and hmac.compare_digest(password, AUTH_PASSWORD):
        return {'user': user, 'role': 'operator'}
    return None
  def _authorized(self):
    return self._auth_identity() is not None
  def _auth_failed(self):
    if self._authorized():
      return False
    self._send_auth_required()
    return True
  def _agent_scope_forbidden(self, path, method):
    identity = self._auth_identity() or {}
    role = str(identity.get('role') or '')
    if role not in {'agent_read', 'agent_operator'}:
      return False
    allowed = path in AGENT_GET_PATHS if method == 'GET' else role == 'agent_operator' and path in AGENT_POST_PATHS
    if allowed:
      return False
    self._send(403, {'error': 'agent_scope_forbidden'})
    return True
  def _strategy_editor_forbidden(self):
    if (self._auth_identity() or {}).get('role') in {'admin', 'operator'}:
      return False
    self._send(403, {'error': 'operator_required'})
    return True
  def do_GET(self):
    parsed = urlparse(self.path)
    if self._auth_failed(): return
    if self._agent_scope_forbidden(parsed.path, 'GET'): return
    if parsed.path == '/api/local/v3/user-source-records':
      self._send(200, {})
  def do_POST(self):
    parsed = urlparse(self.path)
    if self._auth_failed(): return
    if self._agent_scope_forbidden(parsed.path, 'POST'): return
    if parsed.path == '/api/local/copy/generate':
      self._send(200, {})
def main():
  return 0
'''


def basic(user='crm-sidebar', password='synthetic-sidebar-password'):
    return 'Basic ' + base64.b64encode((user + ':' + password).encode()).decode()


class CandidateAuthorizationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        target = os.environ.get('SIDEBAR_CANDIDATE_API')
        cls.text = Path(target).read_text() if target else patch_http(SYNTHETIC_BASE)
        cls.tree = ast.parse(cls.text)
        handler = next(node for node in cls.tree.body if isinstance(node, ast.ClassDef) and any(isinstance(x, ast.FunctionDef) and x.name == 'do_GET' for x in node.body))
        wanted = {'_send', '_send_auth_required', '_auth_identity', '_authorized', '_auth_failed', '_agent_scope_forbidden', '_strategy_editor_forbidden', '_sidebar_api_forbidden', 'do_GET', 'do_POST'}
        methods = [copy.deepcopy(node) for node in handler.body if isinstance(node, ast.FunctionDef) and node.name in wanted]
        cls.namespace = {'base64': base64, 'hmac': hmac, 'json': json, 'urlparse': urlparse, 'parse_qs': parse_qs, 'CORS_ORIGIN': '',
                         'AUTH_USER': 'daru', 'AUTH_PASSWORD': 'synthetic-operator-password', 'ADMIN_AUTH_USER': 'owner', 'ADMIN_AUTH_PASSWORD': 'synthetic-admin-password',
                         'ACCESS_TOKEN': 'synthetic-admin-token', 'AGENT_READ_TOKEN': 'synthetic-read-token', 'AGENT_OPERATOR_TOKEN': 'synthetic-agent-token',
                         'SIDEBAR_AUTH_USER': 'crm-sidebar', 'SIDEBAR_AUTH_PASSWORD': 'synthetic-sidebar-password', 'AUTH_ENABLED': True, 'MAX_REQUEST_BYTES': 4 * 1024 * 1024}
        for name in ['AGENT_GET_PATHS', 'AGENT_POST_PATHS']:
            node = next(node for node in cls.tree.body if isinstance(node, ast.Assign) and any(isinstance(x, ast.Name) and x.id == name for x in node.targets))
            value = node.value
            if isinstance(value, ast.Call) and isinstance(value.func, ast.Name) and value.func.id in {'set', 'frozenset'} and len(value.args) == 1 and not value.keywords:
                cls.namespace[name] = {'set': set, 'frozenset': frozenset}[value.func.id](ast.literal_eval(value.args[0]))
            else:
                cls.namespace[name] = ast.literal_eval(value)
            if not isinstance(cls.namespace[name], (set, frozenset)) or not cls.namespace[name] or not all(isinstance(path, str) for path in cls.namespace[name]):
                raise ValueError('agent_scope_literal_required')
        validator = copy.deepcopy(next(node for node in cls.tree.body if isinstance(node, ast.FunctionDef) and node.name == 'validate_sidebar_auth_configuration'))
        synthetic_class = ast.ClassDef(name='CompiledHandler', bases=[], keywords=[], body=methods, decorator_list=[])
        module = ast.fix_missing_locations(ast.Module(body=[validator, synthetic_class], type_ignores=[]))
        exec(compile(module, '<exact-candidate-handler>', 'exec'), cls.namespace)
        cls.Handler = cls.namespace['CompiledHandler']

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.cache = self.root / 'copy-cache'; self.cache.mkdir(mode=0o700)
        self.snapshot = fixture(); self.engine = Engine()
        self.namespace.update(ROOT=self.root, COPY_CACHE_DIR=self.cache, SIDEBAR_AUTH_USER='crm-sidebar', SIDEBAR_AUTH_PASSWORD='synthetic-sidebar-password')
        def reader(root, customer, corp, employee):
            if str(customer) != str(self.snapshot['customer_id']) or corp != self.snapshot['corp_id'] or employee != self.snapshot['employee_id']:
                raise sidebar_ai_reader.ReadUnavailable('source_identity_unavailable', 404)
            return {'ok': True, 'snapshot': copy.deepcopy(self.snapshot)}
        self.reader = reader
        def generator(root, data, cache):
            return sidebar_manual_generation.generate(data, reader=lambda customer, corp, employee: reader(root, customer, corp, employee), engine=self.engine, cache_dir=cache, enabled=True)
        self.patches = [patch.object(sidebar_ai_reader, 'read_from_published', reader), patch.object(sidebar_manual_generation, 'generation_from_published', generator)]
        for item in self.patches: item.start()

    def tearDown(self):
        for item in reversed(self.patches): item.stop()
        self.temp.cleanup()

    def call(self, method, path, authorization=basic(), data=None):
        handler = self.Handler(); handler.path = path
        body = json.dumps(data or {}).encode()
        handler.headers = {'Content-Type': 'application/json', 'Content-Length': str(len(body))}
        if authorization is not None: handler.headers['Authorization'] = authorization
        handler.rfile = io.BytesIO(body); handler.wfile = io.BytesIO(); handler.status = None; handler.response_headers = {}
        handler.send_response = lambda status: setattr(handler, 'status', status)
        handler.send_header = lambda key, value: handler.response_headers.update({key: value})
        handler.end_headers = lambda: None
        getattr(handler, 'do_' + method)()
        raw = handler.wfile.getvalue()
        try:
            payload = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            payload = None  # Existing WWW-Authenticate responses may be text.
        return handler.status, payload, handler

    def test_dedicated_account_can_read_and_generate_only_scoped_snapshot(self):
        status, result, _ = self.call('GET', '/api/local/v3/sidebar-ai-operations?customer_id=42&corp_id=synthetic-corp&employee_id=synthetic-employee')
        self.assertEqual(status, 200); self.assertEqual(result['snapshot'], self.snapshot)
        status, result, _ = self.call('POST', '/api/local/v3/sidebar-manual-generation', data=request(self.snapshot))
        self.assertEqual(status, 200); self.assertFalse(result['automatic_send_allowed']); self.assertEqual(self.engine.calls, 1)

    def test_other_api_paths_and_wrong_methods_are_forbidden_before_owner_calls(self):
        for path in ['/api/local/v3/user-source-records', '/api/local/copy/generate', '/api/local/v3/strategy-draft', '/api/local/v3/user-orders', '/api/local/unknown', '/api/local/agent/read']:
            for method in ['GET', 'POST']:
                with self.subTest(path=path, method=method): self.assertEqual(self.call(method, path)[0], 403)
        self.assertEqual(self.call('POST', '/api/local/v3/sidebar-ai-operations')[0], 403)
        self.assertEqual(self.call('GET', '/api/local/v3/sidebar-manual-generation')[0], 403)
        self.assertEqual(self.engine.calls, 0)

    def test_all_registered_protected_paths_are_denied_to_dedicated_identity(self):
        allowed = {('GET', '/api/local/v3/sidebar-ai-operations'), ('POST', '/api/local/v3/sidebar-manual-generation')}
        public = {'/api/local/health'}
        for method in ['GET', 'POST']:
            handler = self.Handler(); handler.headers = {'Authorization': basic()}; responses=[]; handler._send=lambda status, body: responses.append(status)
            function = next(node for node in ast.walk(self.tree) if isinstance(node, ast.FunctionDef) and node.name == 'do_' + method)
            paths = {node.value for node in ast.walk(function) if isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value.startswith('/api/local/')}
            for path in paths - public:
                with self.subTest(method=method, path=path): self.assertEqual(handler._agent_scope_forbidden(path, method), (method, path) not in allowed)

    def test_missing_wrong_or_malformed_auth_never_calls_domain(self):
        for authorization in [None, basic(password='wrong'), basic(user='another-user'), 'Basic !!!', 'Basic ' + base64.b64encode(b'missing-colon').decode()]:
            with self.subTest(authorization=authorization): self.assertEqual(self.call('POST', '/api/local/v3/sidebar-manual-generation', authorization, request())[0], 401)
        self.assertEqual(self.engine.calls, 0)

    def test_dedicated_password_comparison_accepts_utf8_without_disclosing_it(self):
        self.namespace['SIDEBAR_AUTH_PASSWORD']='synthetic-秘密'
        path='/api/local/v3/sidebar-ai-operations?customer_id=42&corp_id=synthetic-corp&employee_id=synthetic-employee'
        self.assertEqual(self.call('GET',path,basic(password='synthetic-秘密'))[0],200)
        self.assertEqual(self.call('GET',path,basic(password='synthetic-错误'))[0],401)

    def test_existing_agents_do_not_gain_sidebar_capabilities(self):
        for token in ['synthetic-read-token', 'synthetic-agent-token']:
            self.assertEqual(self.call('GET', '/api/local/v3/sidebar-ai-operations', 'Bearer ' + token)[0], 403)
            self.assertEqual(self.call('POST', '/api/local/v3/sidebar-manual-generation', 'Bearer ' + token, request())[0], 403)
        self.assertEqual(self.engine.calls, 0)

    def test_admin_and_operator_keep_existing_identity_and_sidebar_read(self):
        path='/api/local/v3/sidebar-ai-operations?customer_id=42&corp_id=synthetic-corp&employee_id=synthetic-employee'
        for user, password, role in [('daru', 'synthetic-operator-password', 'operator'), ('owner', 'synthetic-admin-password', 'admin')]:
            _, _, handler=self.call('GET', path, basic(user, password));self.assertEqual(handler._auth_identity()['role'], role)
            self.assertFalse(handler._strategy_editor_forbidden())
            self.assertFalse(handler._agent_scope_forbidden('/api/local/v3/strategy-draft', 'POST'))

    def test_unconfigured_account_and_name_collision_fail_closed(self):
        self.namespace['SIDEBAR_AUTH_PASSWORD']=''
        self.assertEqual(self.call('POST', '/api/local/v3/sidebar-manual-generation', data=request())[0], 401)
        self.namespace['validate_sidebar_auth_configuration']()
        self.namespace['SIDEBAR_AUTH_PASSWORD']='synthetic-sidebar-password'
        for name in ['', 'daru', 'owner', ' crm-sidebar', 'crm:sidebar']:
            self.namespace['SIDEBAR_AUTH_USER']=name
            with self.assertRaisesRegex(RuntimeError, 'sidebar_auth_configuration_invalid'):self.namespace['validate_sidebar_auth_configuration']()

    def test_invalid_queries_and_cross_scope_never_generate(self):
        for query, status in [('customer_id=42&corp_id=synthetic-corp',400), ('customer_id=42&customer_id=43&corp_id=synthetic-corp&employee_id=synthetic-employee',400), ('customer_id=43&corp_id=synthetic-corp&employee_id=synthetic-employee',404), ('customer_id=42&corp_id=other&employee_id=synthetic-employee',404), ('customer_id=42&corp_id=synthetic-corp&employee_id=other',404)]:
            with self.subTest(query=query):self.assertEqual(self.call('GET','/api/local/v3/sidebar-ai-operations?'+query)[0],status)
        for field in ['customer_id','corp_id','employee_id','summary']:
            data=request();data['snapshot'][field]='changed'
            self.assertIn(self.call('POST','/api/local/v3/sidebar-manual-generation',data=data)[0],{404,409})
        self.assertEqual(self.engine.calls,0)

    def test_http_repeat_and_unknown_preserve_durable_no_retry(self):
        path='/api/local/v3/sidebar-manual-generation'
        self.assertEqual(self.call('POST',path,data=request())[0],200)
        self.assertEqual(self.call('POST',path,data=request())[0],200)
        self.assertEqual(self.engine.calls,1)
        self.snapshot['summary']='new synthetic summary';data=request(self.snapshot);data['idempotency_key']='synthetic-unknown-key'
        def fail(snapshot):raise TimeoutError('private synthetic error')
        self.engine=Engine(fail)
        status,result,_=self.call('POST',path,data=data)
        self.assertEqual(status,504);self.assertEqual(result['error'],'generation_outcome_unknown')
        self.engine=Engine()
        self.assertEqual(self.call('POST',path,data=data)[0],409)
        self.assertEqual(self.engine.calls,0)
        for file in self.cache.glob('*.json'):
            self.assertEqual(file.stat().st_mode&0o777,0o600)
            self.assertNotIn('synthetic-sidebar-password',file.read_text())
            self.assertNotIn('private synthetic error',file.read_text())

    def test_startup_auth_validation_precedes_any_resume_function(self):
        main=next(node for node in self.tree.body if isinstance(node,ast.FunctionDef) and node.name=='main')
        self.assertEqual(ast.unparse(main.body[0]),'validate_sidebar_auth_configuration()')


if __name__=='__main__': unittest.main()
