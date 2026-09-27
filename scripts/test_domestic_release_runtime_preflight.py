"""Real subprocess contracts for actual TMPDIR and DevTools prerequisites."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from scripts import domestic_main_release as release


class RuntimePreflightTest(unittest.TestCase):
    def test_timeout_kills_the_entire_isolated_command_group(self):
        with tempfile.TemporaryDirectory() as raw:
            marker=Path(raw)/'child.pid'
            child="import os,time;from pathlib import Path;Path("+repr(str(marker))+").write_text(str(os.getpid()));time.sleep(120)"
            parent="import subprocess,time;subprocess.Popen(["+repr(sys.executable)+",'-c',"+repr(child)+"]);time.sleep(120)"
            with self.assertRaises(subprocess.TimeoutExpired):
                release._run([sys.executable,'-c',parent],timeout=2,terminate_group=True)
            self.assertTrue(marker.exists())
            pid=int(marker.read_text())
            for _ in range(100):
                state=subprocess.run(['ps','-o','stat=','-p',str(pid)],capture_output=True,text=True).stdout.strip()
                if not state or state.startswith('Z'):break
                time.sleep(0.02)
            self.assertTrue(not state or state.startswith('Z'),state)

    def run_probe(self, fatal=False):
        with tempfile.TemporaryDirectory(prefix="ac-", dir=Path("/tmp").resolve()) as raw:
            root=Path(raw);root.chmod(0o700)
            report=root/"report";report.mkdir()
            binary=root/"fake-chrome";server=root/"server.py"
            binary.write_text("#!/bin/sh\nexec "+shlex.quote(sys.executable)+" "+shlex.quote(str(server))+" \"$@\"\n")
            binary.chmod(0o700)
            server.write_text('''import json,os,sys
from http.server import BaseHTTPRequestHandler,HTTPServer
from pathlib import Path
if '--version' in sys.argv:
 print('fixture Chrome');raise SystemExit(0)
assert 'AICRM_DATABASE_URL' not in os.environ
profile=Path(next(a.split('=',1)[1] for a in sys.argv if a.startswith('--user-data-dir=')))
if os.environ.get('FIXTURE_FATAL')=='1':
 print('FATAL first startup cause Socket path too long',file=sys.stderr)
 print('later cpu-frequency warning '*2000,file=sys.stderr);raise SystemExit(23)
class Handler(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(json.dumps({'webSocketDebuggerUrl':'ws://127.0.0.1/fixture'}).encode())
 def log_message(self,*a):pass
server=HTTPServer(('127.0.0.1',0),Handler)
(profile/'DevToolsActivePort').write_text(str(server.server_port)+'\\n')
server.serve_forever()
''')
            def build(config,args,**kwargs):
                env=dict(os.environ,TMPDIR=raw,AICRM_CHROMIUM_BINARY=str(binary),
                         AICRM_DATABASE_URL='must-not-reach-browser',FIXTURE_FATAL='1' if fatal else '0')
                return subprocess.run(args,cwd=kwargs['cwd'],env=env,capture_output=True,text=True,
                                      timeout=kwargs['timeout'],check=kwargs['check'])
            policy=Path(__file__).resolve().parents[1]
            with patch.object(release,'_build_command',side_effect=build):
                if fatal:
                    with self.assertRaises(release.CheckEnvironmentError):
                        release._check_execution_preflight({},policy,root,report,True)
                else:
                    identity=release._check_execution_preflight({},policy,root,report,True)
                    self.assertTrue(identity['chromium']['devtools_ready'])
            evidence=json.loads((report/'browser-precheck.json').read_text())
            self.assertEqual(list(root.glob('p-*')),[])
            self.assertEqual((report/'browser-precheck.json').stat().st_mode&0o777,0o600)
            return evidence

    def test_devtools_ready_under_actual_short_private_tmpdir_and_profile_cleanup(self):
        self.assertTrue(self.run_probe()['devtools_ready'])

    def test_early_fatal_preserved_when_later_warnings_fill_stderr(self):
        evidence=self.run_probe(fatal=True)
        self.assertFalse(evidence['devtools_ready'])
        self.assertIn('first startup cause',evidence['stderr_first'])
        self.assertNotIn('first startup cause',evidence['stderr_tail'])
        self.assertEqual(evidence['exit_code'],23)


if __name__=='__main__':unittest.main()
