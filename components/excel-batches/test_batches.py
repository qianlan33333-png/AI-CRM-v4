import base64
import io
import json
import tempfile
import threading
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from openpyxl import Workbook

from batches import (
    DELIVERY_LABELS,
    HEADERS,
    Handler,
    Invalid,
    Service,
    Source,
    WINDOW_LABELS,
    _report_label,
    Conflict,
    content_key,
    parse_excel,
)


PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="
)


def workbook(rows, header=HEADERS):
    book = Workbook()
    sheet = book.active
    sheet.append(header)
    for row in rows:
        sheet.append(row)
    out = io.BytesIO()
    book.save(out)
    return out.getvalue()


class FakeSource:
    def __init__(self):
        self.events = {}
        self.calls = []

    def opens(self, unionid, path, start, end):
        self.calls.append((unionid, path, start, end))
        return self.events.get(unionid)


class Tests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = FakeSource()
        self.config = {"appid": "fixture-app"}
        self.service = Service(self.root / "data.sqlite", self.config, self.source)

    def test_parse_by_column_name_optional_segment_and_title_empty(self):
        header = ["标题", "分层", "发送人 userid", "unionid", "小程序 path", "话术"]
        rows = parse_excel(
            workbook(
                [["标题 A", "A", "staff-1", "00123", "pages/a/a", "copy"], [None, "", "staff-2", "u2", "pages/b/b", "copy2"]],
                header,
            )
        )
        self.assertEqual(rows[0]["unionid"], "00123")
        self.assertEqual(rows[0]["segment"], "A")
        self.assertIsNone(rows[1]["segment"])
        self.assertEqual(rows[1]["title"], "")

    def test_prepare_is_pure_and_carries_segment_metadata(self):
        raw = workbook([["u", "copy", "pages/a/a", "staff", "title", "B"]], HEADERS + ["分层"])
        prepared = self.service.prepare_import(raw)
        self.assertEqual(prepared["segment_source"], "excel")
        self.assertTrue(prepared["segment_column_present"])
        self.assertTrue(prepared["has_segments"])
        self.assertEqual(prepared["rows"][0]["segment"], "B")
        with self.service.db() as db:
            self.assertEqual(db.execute("SELECT count(*) FROM imports").fetchone()[0], 0)

    def test_optional_segment_column_missing_has_no_unknown_group(self):
        rows = parse_excel(workbook([["u", "copy", "pages/a/a", "staff", "title"]]))
        self.assertNotIn("unknown", rows[0])
        imported = self.service.import_file(workbook([["u", "copy", "pages/a/a", "staff", "title"]]))
        self.assertFalse(imported["has_segments"])
        self.assertFalse(imported["segment_column_present"])
        snapshot = self.service.snapshot("snap-no-segment", [{"id": 1, "unionid": "u"}])
        self.assertFalse(snapshot["has_segments"])
        report = self.service.observe(1, "snap-no-segment", [{"id": 1, "unionid": "u", "path": "pages/a/a", "state": "delivery_proven", "sent_at": "2026-09-01T00:00:00Z"}], "2026-09-03T00:00:00Z")
        self.assertFalse(report["has_segments"])
        self.assertEqual(report["windows"]["12"]["groups"], {})
        self.assertEqual(sorted(report["overall"]), ["12", "24", "48"])

    def test_invalid_segment_and_duplicate_unionid_report_excel_row(self):
        bad = workbook([["u", "copy", "pages/a/a", "staff", "title", "Z"]], HEADERS + ["分层"])
        with self.assertRaises(Invalid) as error:
            parse_excel(bad)
        self.assertEqual(error.exception.row_errors, [{"row": 2, "field": "分层", "code": "invalid_segment"}])
        duplicate = workbook([["u", "copy", "pages/a/a", "staff", "title"], ["u", "copy2", "pages/b/b", "staff", "title2"]])
        with self.assertRaises(Invalid) as error:
            parse_excel(duplicate)
        self.assertEqual(error.exception.row_errors[0]["row"], 3)
        self.assertEqual(error.exception.row_errors[0]["code"], "duplicate_unionid")

    def test_snapshot_uses_input_segment_and_is_immutable(self):
        # FakeSource deliberately has no segment method. A call would be an
        # accidental regression to the removed segment_sql dependency.
        rows = [{"id": 1, "unionid": "u1", "segment": "A"}, {"id": 2, "unionid": "u2", "segment": ""}]
        first = self.service.snapshot("frozen", rows, segment_source="excel", has_segments=True, version=3)
        self.assertEqual(first["groups"], {"1": "A", "2": None})
        with self.assertRaises(Conflict):
            self.service.snapshot("frozen", [{"id": 1, "unionid": "u1", "segment": "B"}, {"id": 2, "unionid": "u2", "segment": ""}], segment_source="excel", has_segments=True, version=3)

    def test_report_overall_group_conservation_late_data_and_missing_rate(self):
        start = datetime(2026, 9, 1, tzinfo=timezone.utc)
        rows = [
            {"id": 1, "unionid": "u1", "segment": "A", "path": "pages/article/article?lesson_id=12", "state": "delivery_proven", "sent_at": start.isoformat()},
            {"id": 2, "unionid": "u2", "segment": "B", "path": "pages/article/article?lesson_id=12", "state": "delivery_proven", "sent_at": (start + timedelta(hours=20)).isoformat()},
            {"id": 3, "unionid": "u3", "segment": "", "path": "pages/article/article?lesson_id=12", "state": "delivery_proven", "sent_at": start.isoformat()},
            {"id": 4, "unionid": "u4", "segment": "C", "path": "pages/article/article?lesson_id=12", "state": "provider_accepted", "sent_at": None},
        ]
        self.service.snapshot("approval-v3", rows, segment_source="excel", has_segments=True, version=3)
        self.source.events = {
            "u1": [(start + timedelta(hours=12)).isoformat()] * 3,
            "u2": [],
            "u3": None,  # full-window source missing: do not turn it into zero
        }
        report = self.service.observe(1, "approval-v3", rows, start + timedelta(hours=70))
        self.assertEqual(report["overall"]["12"]["sent"], 3)
        self.assertEqual(list(report["windows"]["12"]["groups"]), ["A", "B", "C", "unsegmented"])
        for hours in ("12", "24", "48"):
            window = report["windows"][hours]
            self.assertEqual(sum(stats["sent"] for stats in window["groups"].values()), window["overall"]["sent"])
        overall_12 = report["windows"]["12"]["overall"]
        segment_a_12 = report["windows"]["12"]["groups"]["A"]
        segment_b_12 = report["windows"]["12"]["groups"]["B"]
        segment_empty_12 = report["windows"]["12"]["groups"]["unsegmented"]
        self.assertEqual(overall_12["matured"], 3)
        self.assertEqual(segment_a_12["opened"], 1)
        self.assertEqual(segment_b_12["opened"], 0)
        self.assertEqual(segment_empty_12["unavailable"], 1)
        self.assertIsNone(overall_12["open_rate"])
        self.assertEqual(segment_a_12["open_rate"], 1.0)
        self.assertEqual(segment_b_12["open_rate"], 0.0)
        self.assertEqual(report["rows"][2]["segment"], None)

        restarted = Service(self.root / "data.sqlite", self.config, self.source)
        persisted = restarted.report(1)
        self.assertEqual(persisted["windows"]["12"]["overall"], report["windows"]["12"]["overall"])
        self.assertEqual(persisted["windows"]["12"]["groups"].keys(), report["windows"]["12"]["groups"].keys())

        # A late event is read again on the next pass and can change only the
        # windows whose inclusive end boundary contains it.
        self.source.events["u2"] = [(start + timedelta(hours=55)).isoformat()]
        late = self.service.observe(1, "approval-v3", rows, start + timedelta(hours=100))
        self.assertEqual(late["windows"]["24"]["groups"]["B"]["opened"], 0)
        self.assertEqual(late["windows"]["48"]["groups"]["B"]["opened"], 1)

    def test_boundary_and_observing_windows(self):
        start = datetime(2026, 9, 1, tzinfo=timezone.utc)
        row = {"id": 1, "unionid": "u", "segment": "A", "path": "pages/article/article?lesson_id=1", "state": "delivery_proven", "sent_at": start.isoformat()}
        self.service.snapshot("boundary", [row], segment_source="excel", has_segments=True)
        self.source.events["u"] = [(start + timedelta(hours=12)).isoformat()]
        at_boundary = self.service.observe(1, "boundary", [row], start + timedelta(hours=12))
        self.assertEqual(at_boundary["windows"]["12"]["overall"]["matured"], 1)
        self.assertEqual(at_boundary["windows"]["12"]["overall"]["opened"], 1)
        self.assertIsNone(at_boundary["windows"]["24"]["overall"]["open_rate"])
        self.assertEqual(at_boundary["rows"][0]["windows"]["24"], "observing")

    def test_idempotency_conflict_and_restart_replay(self):
        raw = workbook([["u", "copy", "pages/a/a", "staff", "title"]])
        first = self.service.import_file(raw, new_batch=True, request_key="new-key-01")
        replay = self.service.import_file(raw, new_batch=True, request_key="new-key-01")
        self.assertEqual(replay["batch_key"], first["batch_key"])
        with self.assertRaises(Conflict) as error:
            self.service.import_file(workbook([["v", "copy", "pages/a/a", "staff", "title"]]), new_batch=True, request_key="new-key-01")
        self.assertEqual(error.exception.code, "idempotency_conflict")
        restarted = Service(self.root / "data.sqlite", self.config, self.source)
        self.assertEqual(restarted.import_file(raw, new_batch=True, request_key="new-key-01")["batch_key"], first["batch_key"])

    def test_csv_formula_injection_is_escaped(self):
        start = datetime(2026, 9, 30, 16, 0, 0, 611265, tzinfo=timezone.utc)
        rows = [
            {"id": 1, "unionid": "=evil", "text": "+evil", "segment": "A", "path": "@pages/a/a?x=1", "state": "delivery_proven", "sent_at": start.isoformat()},
            {"id": 2, "unionid": "plain", "text": "copy", "segment": "", "path": "pages/a/a", "state": "outcome_unknown", "sent_at": None},
            {"id": 3, "unionid": "invalid", "text": "copy", "segment": "B", "path": "pages/a/a", "state": "unmapped_state", "sent_at": "2026-02-31T00:00:00Z"},
        ]
        self.service.snapshot("csv", rows, segment_source="excel", has_segments=True)
        self.source.events["=evil"] = []
        self.service.observe(1, "csv", rows, start + timedelta(hours=49))
        csv_bytes = self.service.report_csv(1).decode()
        self.assertIn("行 ID,接收人 UnionID,话术,发送员工 UserID,小程序路径,分层,发送状态,实际发送时间,12 小时观察,24 小时观察,48 小时观察", csv_bytes)
        self.assertIn("'=evil", csv_bytes)
        self.assertIn("'+evil", csv_bytes)
        self.assertIn("'@pages/a/a?x=1", csv_bytes)
        self.assertIn("发送成功,2026-10-01 00:00:00,未打开,未打开,未打开", csv_bytes)
        self.assertIn("结果待核实,,,,", csv_bytes)
        self.assertIn("状态待核对,时间暂时无法显示", csv_bytes)
        self.assertNotIn("2026-09-30T16:00:00.611265+00:00", csv_bytes)
        self.assertEqual(
            _report_label("provider_accepted", DELIVERY_LABELS),
            "任务已创建，待员工执行",
        )
        self.assertEqual(
            [_report_label(value, WINDOW_LABELS) for value in ("observing", "opened", "not_opened", "unavailable")],
            ["观察中", "已打开", "未打开", "暂不可统计"],
        )

    def test_source_coverage_is_required_for_zero_open(self):
        source = Source({})
        coverage = [None]

        def query(name, params):
            if name == "coverage_sql":
                return [] if coverage[0] is None else [{"complete": coverage[0]}]
            if name == "user_sql":
                return [{"user_id": 7}]
            return []

        source.query = query
        now = datetime.now(timezone.utc)
        args = ("u", "pages/article/article?lesson_id=1", now, now)
        self.assertIsNone(source.opens(*args))
        coverage[0] = 0
        self.assertIsNone(source.opens(*args))
        coverage[0] = 1
        self.assertEqual(source.opens(*args), [])

    def test_direct_content_open_requires_coverage_before_zero(self):
        service = Service(self.root / "direct-content.sqlite", self.config)
        service.source.opens = lambda *_: None
        query = {"unionid": "union", "path": "pages/article/article?lesson_id=1", "start": "2026-01-01T00:00:00Z", "end": "2026-01-02T00:00:00Z"}
        self.assertEqual(service.content_open(query)["state"], "unavailable")
        service.source.opens = lambda *_: []
        self.assertEqual(service.content_open(query)["state"], "not_opened")

    def test_authenticated_http_and_row_errors(self):
        from http.server import ThreadingHTTPServer

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        server.service = Service(self.root / "http.sqlite", self.config)
        server.token = "fixture-" * 8
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            base = "http://127.0.0.1:" + str(server.server_port)
            with self.assertRaises(HTTPError) as rejected:
                urlopen(base + "/health")
            self.assertEqual(rejected.exception.code, 401)

            def call(path, body=None, method="POST", headers=None):
                request_headers = {"Authorization": "Bearer " + server.token}
                if headers:
                    request_headers.update(headers)
                request = Request(base + path, data=body, headers=request_headers, method=method)
                with urlopen(request) as response:
                    return response.status, response.read(), response.headers

            status, body, _ = call("/imports", workbook([["u", "copy", "pages/a/a", "staff", "title"]]))
            self.assertEqual(status, 200)
            imported = json.loads(body)
            self.assertFalse(imported["has_segments"])
            status, body, _ = call("/covers", PNG)
            self.assertEqual(status, 200)
            self.assertEqual(json.loads(body)["cover_digest"].startswith("sha256:"), True)
            bad = workbook([["u", "copy", "pages/a/a", "staff", "title", "Z"]], HEADERS + ["分层"])
            with self.assertRaises(HTTPError) as invalid:
                call("/imports?new=1", bad, headers={"Idempotency-Key": "http-new-01"})
            self.assertEqual(invalid.exception.code, 400)
            error_body = json.loads(invalid.exception.read())
            self.assertEqual(error_body["row_errors"][0]["row"], 2)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_path_only_known_content_routes(self):
        self.assertEqual(content_key("pages/article/article?lesson_id=55&from=learn"), ("lesson", "55"))
        self.assertIsNone(content_key("pages/home/index"))
        self.assertIsNone(content_key("pages/article/article?lesson_id=1&lesson_id=2"))


if __name__ == "__main__":
    unittest.main()
