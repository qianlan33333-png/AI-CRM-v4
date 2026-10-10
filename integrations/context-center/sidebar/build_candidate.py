"""Build a separate review directory from the exact verified running sources.

No active source, configuration, permission, service or database is changed.
"""
import ast
import hashlib
from pathlib import Path

BASE_SHA256 = {
    "export-crm-production-read-facts.py": "972b5abe5014cfceb04316443c2507576cf670eb0ef027cca351ef7c53e9ac89",
    "local-api.py": "11884412cae51a2fc67b29566310fe00c16b666f6535c1a835c7594f94c49c53",
    "warehouse-archive-refresh.py": "db292b5a59617e554523ecee5d8cf071636438c00c32cafb22e46682f11ff998",
}

PRIVATE_MESSAGE_COLUMNS = """      p.participant_role,m.conversation_type,m.corp_scope,
      coalesce((select jsonb_agg(staff.provider_value order by staff.provider_value) from
        (select distinct provider_value from message_archive_participants
         where message_id=m.id and actor_type='staff' and m.conversation_type='private')staff),'[]'::jsonb) staff_provider_user_ids,
      (select count(*) from message_archive_participants where message_id=m.id) participant_count,
      (select case when count(*)=1 then min(actor_type) else 'ambiguous' end
         from message_archive_participants where message_id=m.id and participant_role='sender') sender_actor_type"""


def patched_sql(sql):
    marker = " 'contract','crm-production-read-facts.v1','complete',true,'read_only',true,"
    columns = "      p.participant_role,m.conversation_type"
    if sql.count(marker) != 1 or sql.count(columns) != 1:
        raise ValueError("export_sql_base_mismatch")
    return sql.replace(marker, marker + "\n 'message_scope_version','crm-private-message-scope.v1',").replace(columns, PRIVATE_MESSAGE_COLUMNS)


def _replace_node(text, node, replacement):
    lines = text.splitlines(keepends=True)
    return "".join(lines[:node.lineno - 1]) + replacement.rstrip() + "\n" + "".join(lines[node.end_lineno:])


def patch_export(text):
    nodes = [node for node in ast.parse(text).body if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == "SQL" for target in node.targets)]
    if len(nodes) != 1 or not isinstance(nodes[0].value, ast.Constant):
        raise ValueError("export_sql_base_mismatch")
    sql = patched_sql(nodes[0].value.value)
    if '"""' in sql:
        raise ValueError("export_sql_literal_invalid")
    text = _replace_node(text, nodes[0], 'SQL = r"""' + sql + '"""')
    function = next(node for node in ast.parse(text).body if isinstance(node, ast.FunctionDef) and node.name == "validate_facts")
    insertion = next(node for node in function.body if isinstance(node, ast.Assign) and any(isinstance(target, ast.Name) and target.id == "timestamp" for target in node.targets))
    guard = """if facts.get('message_scope_version') != 'crm-private-message-scope.v1':
    raise ValueError('production_message_scope_unavailable')
for row in facts['messages']:
    if not isinstance(row, dict) or (row.get('conversation_type') == 'private' and not {'corp_scope', 'staff_provider_user_ids', 'participant_count', 'sender_actor_type'}.issubset(row)):
        raise ValueError('production_message_scope_unavailable')
"""
    lines = text.splitlines(keepends=True)
    return "".join(lines[:insertion.lineno - 1]) + "".join(" " * insertion.col_offset + line + "\n" for line in guard.splitlines()) + "".join(lines[insertion.lineno - 1:])


def patch_http(text):
    tree = ast.parse(text)
    handler = next(node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef) and node.name == "do_GET")
    targets = [node for node in ast.walk(handler) if isinstance(node, ast.If) and isinstance(node.test, ast.Compare)
               and any(isinstance(value, ast.Constant) and value.value == "/api/local/v3/user-source-records" for value in node.test.comparators)]
    if len(targets) != 1:
        raise ValueError("http_route_base_mismatch")
    indent = " " * targets[0].col_offset
    branch = """if parsed.path == '/api/local/v3/sidebar-ai-operations':
  if self._strategy_editor_forbidden():
    return
  from sidebar_ai_reader import read_from_published, ReadUnavailable
  try:
    params = parse_qs(parsed.query)
    if set(params) != {'customer_id', 'corp_id', 'employee_id'} or any(len(values) != 1 for values in params.values()):
      raise ReadUnavailable('request_scope_invalid', 400)
    payload = read_from_published(ROOT, params['customer_id'][0], params['corp_id'][0], params['employee_id'][0])
    self._send(200, payload)
  except ReadUnavailable as exc:
    self._send(exc.status, {'ok': False, 'error': exc.code, 'automatic_send_allowed': False})
  return
"""
    lines = text.splitlines(keepends=True)
    text = "".join(lines[:targets[0].lineno - 1]) + "".join(indent + line + "\n" for line in branch.splitlines()) + "".join(lines[targets[0].lineno - 1:])
    tree = ast.parse(text)
    post = next(node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef) and node.name == "do_POST")
    targets = [node for node in ast.walk(post) if isinstance(node, ast.If) and isinstance(node.test, ast.Compare)
               and any(isinstance(value, ast.Constant) and value.value == "/api/local/copy/generate" for value in node.test.comparators)]
    if len(targets) != 1:
        raise ValueError("generation_route_base_mismatch")
    indent = " " * targets[0].col_offset
    branch = """if parsed.path == '/api/local/v3/sidebar-manual-generation':
  if self._strategy_editor_forbidden():
    return
  from sidebar_manual_generation import generation_from_published
  from sidebar_ai_reader import ReadUnavailable
  try:
    size = int(self.headers.get('Content-Length') or '0')
    if parsed.query or not 0 < size <= min(4 * 1024 * 1024, MAX_REQUEST_BYTES) or self.headers.get('Content-Type', '').split(';')[0] != 'application/json':
      raise ReadUnavailable('generation_input_invalid', 400)
    data = json.loads(self.rfile.read(size).decode('utf-8'))
    self._send(200, generation_from_published(ROOT, data, COPY_CACHE_DIR))
  except ReadUnavailable as exc:
    self._send(exc.status, {'ok': False, 'error': exc.code, 'automatic_send_allowed': False})
  except (ValueError, TypeError, UnicodeError):
    self._send(400, {'ok': False, 'error': 'generation_input_invalid', 'automatic_send_allowed': False})
  except Exception:
    self._send(503, {'ok': False, 'error': 'generation_unavailable', 'automatic_send_allowed': False})
  return
"""
    lines = text.splitlines(keepends=True)
    return "".join(lines[:targets[0].lineno - 1]) + "".join(indent + line + "\n" for line in branch.splitlines()) + "".join(lines[targets[0].lineno - 1:])


def patch_refresh(text):
    tree = ast.parse(text)
    run = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "run")
    replacement = """from refresh_diagnostics import StepFailure, checked_step

def run(command: list[str], *, env: dict[str, str] | None = None, timeout: int = 1800) -> dict:
    return checked_step(command, cwd=ROOT, env=env, timeout=timeout)
"""
    text = _replace_node(text, run, replacement)
    tree = ast.parse(text)
    targets = [node for node in ast.walk(tree) if isinstance(node, ast.ExceptHandler) and node.name == "exc"
               and any(isinstance(statement, ast.Expr) and isinstance(statement.value, ast.Call)
                       and ast.unparse(statement.value.func) == "report.update" for statement in node.body)]
    if len(targets) != 1:
        raise ValueError("refresh_receipt_base_mismatch")
    first = targets[0].body[0]
    indent = " " * first.col_offset
    lines = text.splitlines(keepends=True)
    return "".join(lines[:first.lineno - 1]) + indent + "if isinstance(exc, StepFailure):\n" + indent + "    report['steps'].append(exc.receipt)\n" + "".join(lines[first.lineno - 1:])


PATCHERS = {"export-crm-production-read-facts.py": patch_export, "local-api.py": patch_http, "warehouse-archive-refresh.py": patch_refresh}


def build(source_root, output_root):
    source_root, output_root = Path(source_root).resolve(), Path(output_root).resolve()
    if output_root == source_root or output_root in source_root.parents:
        raise ValueError("separate_candidate_directory_required")
    source_ops = source_root / "ops"
    receipt = {"base": BASE_SHA256, "candidate": {}, "active_source_changed": False}
    prepared = {}
    for name, patcher in PATCHERS.items():
        raw = (source_ops / name).read_bytes()
        if hashlib.sha256(raw).hexdigest() != BASE_SHA256[name]:
            raise ValueError("source_base_changed")
        modified = patcher(raw.decode())
        ast.parse(modified)
        prepared[name] = modified.encode()
        receipt["candidate"][name] = hashlib.sha256(modified.encode()).hexdigest()
    for name in ("sidebar_ai_reader.py", "refresh_diagnostics.py", "sidebar_native_copy.py", "sidebar_manual_generation.py"):
        data = (Path(__file__).parent / "ops" / name).read_bytes()
        prepared[name] = data
        receipt["candidate"][name] = hashlib.sha256(data).hexdigest()
    candidate_ops = output_root / "ops"
    candidate_ops.mkdir(parents=True, exist_ok=False)
    for name, data in prepared.items():
        (candidate_ops / name).write_bytes(data)
    return receipt


if __name__ == "__main__":
    import argparse, json
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--output-root", required=True)
    args = parser.parse_args()
    print(json.dumps(build(args.source_root, args.output_root)))
