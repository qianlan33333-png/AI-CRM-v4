"""Exercise exact old/new message SELECTs in a disposable local PostgreSQL16.

The cluster contains two synthetic tables only. Every connection is explicitly
loopback; existing PostgreSQL credentials/services/client startup files are unused.
"""
import getpass
import json
import os
import pathlib
import socket
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
PG_BIN = pathlib.Path("/opt/homebrew/opt/postgresql@16/bin")


def message_select(sql):
    part = sql.split(" 'messages',(select coalesce(json_agg(x),'[]') from (\n", 1)[1].split(" 'surveys'", 1)[0]
    body = part[:part.rfind(")x),")]
    return "BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT coalesce(json_agg(x),'[]') FROM (\n" + body + ")x;\nROLLBACK;\n"


def run():
    env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    env["PGPASSFILE"] = "/dev/null"
    env["PGSERVICEFILE"] = "/dev/null"
    with socket.socket() as reserve:
        reserve.bind(("127.0.0.1", 0))
        port = reserve.getsockname()[1]
    with tempfile.TemporaryDirectory(prefix="aicrm-reader-sql-") as directory:
        base = pathlib.Path(directory)
        data = base / "data"
        log = base / "postgres.log"
        subprocess.run([str(PG_BIN / "initdb"), "-D", str(data), "-A", "trust", "--no-instructions"], env=env, capture_output=True, check=True)
        started = False
        try:
            subprocess.run([str(PG_BIN / "pg_ctl"), "-D", str(data), "-l", str(log), "-o", f"-h 127.0.0.1 -p {port} -k {base}", "-w", "start"], env=env, capture_output=True, check=True)
            started = True
            cmd = [str(PG_BIN / "psql"), "-X", "-w", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-p", str(port), "-U", getpass.getuser(), "-d", "postgres"]
            fixture = """
CREATE TABLE message_archive_messages(id bigint primary key, occurred_at timestamptz, content_text text, msgtype text, conversation_type text, corp_scope text);
CREATE TABLE message_archive_participants(id bigint primary key, message_id bigint, customer_id_at_ingest bigint, participant_role text, actor_type text, provider_value text);
INSERT INTO message_archive_messages VALUES
 (1,'2026-10-09T01:01:00Z','synthetic sales','text','private','wecom-corp:corp'),
 (2,'2026-10-09T01:02:00Z','synthetic customer','text','private','wecom-corp:corp'),
 (3,'2026-10-09T01:03:00Z','synthetic group','text','group','wecom-corp:corp'),
 (4,'2026-10-09T01:04:00Z','synthetic other staff','text','private','wecom-corp:corp'),
 (5,'2026-10-09T01:05:00Z','synthetic other corp','text','private','wecom-corp:other'),
 (6,'2026-10-09T01:06:00Z','synthetic ambiguous','text','private','wecom-corp:corp');
INSERT INTO message_archive_participants VALUES
 (11,1,null,'sender','staff','staff'),(12,1,42,'recipient','external_customer','synthetic-external'),
 (21,2,42,'sender','external_customer','synthetic-external'),(22,2,null,'recipient','staff','staff'),
 (31,3,42,'sender','external_customer','synthetic-external'),(32,3,null,'recipient','staff','staff'),(33,3,null,'recipient','staff','other-staff'),
 (41,4,42,'sender','external_customer','synthetic-external'),(42,4,null,'recipient','staff','other-staff'),
 (51,5,42,'sender','external_customer','synthetic-external'),(52,5,null,'recipient','staff','staff'),
 (61,6,42,'sender','external_customer','synthetic-external'),(62,6,null,'recipient','staff','staff'),(63,6,43,'recipient','external_customer','synthetic-external-43');
"""
            subprocess.run(cmd, input=fixture, text=True, env=env, capture_output=True, check=True)
            before = json.loads(subprocess.run(cmd, input=message_select((ROOT / "source-export.sql").read_text()), text=True, env=env, capture_output=True, check=True).stdout)
            after = json.loads(subprocess.run(cmd, input=message_select((ROOT / "candidate-export.sql").read_text()), text=True, env=env, capture_output=True, check=True).stdout)
            assert len(before) == len(after) == 7
            old_keys = set(before[0])
            assert [{key: row[key] for key in old_keys} for row in after] == before
            by_id = {row["message_id"]: row for row in after}
            assert by_id[1]["sender_actor_type"] == "staff"
            assert by_id[2]["staff_provider_user_ids"] == ["staff"]
            assert by_id[3]["staff_provider_user_ids"] == []
            assert by_id[4]["staff_provider_user_ids"] == ["other-staff"]
            assert by_id[5]["corp_scope"] == "wecom-corp:other"
            assert by_id[6]["participant_count"] == 3
            return {"status": "passed", "database": "independent synthetic PostgreSQL16 loopback", "old_and_new_rows": len(after), "original_columns_preserved": True, "scope_fields_verified": True, "production_database_connected": False}
        finally:
            if started:
                subprocess.run([str(PG_BIN / "pg_ctl"), "-D", str(data), "-m", "fast", "-w", "stop"], env=env, capture_output=True, check=True)


if __name__ == "__main__":
    receipt = run()
    receipt["temporary_cluster_stopped"] = True
    print(json.dumps(receipt))
