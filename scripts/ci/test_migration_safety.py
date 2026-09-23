from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from migration_safety import dangerous_sql_reasons

ROOT = Path(__file__).resolve().parents[2]
CHECKER = ROOT / "scripts/check-migration-sequence.py"


class MigrationSafetyTest(unittest.TestCase):
    def test_rejects_common_destructive_statements(self):
        examples = {
            "DROP TABLE customer_data;": "dropping a table, view, schema, type, domain, or sequence",
            "ALTER TABLE customer_data\n DROP COLUMN phone;": "dropping a column",
            "ALTER TABLE customer_data RENAME COLUMN phone TO mobile;": "renaming a table or column",
            "TRUNCATE TABLE customer_data;": "truncating a table",
            "DELETE FROM customer_data WHERE inactive=true;": "deleting rows",
            "UPDATE customer_data SET active=false;": "an UPDATE without a WHERE clause",
        }
        for sql, expected in examples.items():
            with self.subTest(sql=sql):
                self.assertIn(expected, dangerous_sql_reasons(sql))

    def test_rejects_destructive_sql_inside_execute_now_do_block(self):
        sql = "DO $migration$ BEGIN DELETE FROM customer_data; END $migration$;"
        self.assertIn("deleting rows", dangerous_sql_reasons(sql))

    def test_rejects_static_destructive_dynamic_sql_inside_do_block(self):
        sql = "DO $migration$ BEGIN EXECUTE 'DROP TABLE customer_data'; END $migration$;"
        self.assertIn("dropping a table, view, schema, type, domain, or sequence",
                      dangerous_sql_reasons(sql))

    def test_ignores_comments_strings_and_function_bodies(self):
        sql = """
        -- DROP TABLE customer_data;
        /* ALTER TABLE customer_data DROP COLUMN phone; */
        INSERT INTO migration_notes(value) VALUES ('TRUNCATE TABLE customer_data');
        CREATE FUNCTION cleanup_preview() RETURNS void LANGUAGE plpgsql AS $$
        BEGIN
          DELETE FROM temporary_preview_rows;
        END;
        $$;
        CREATE TRIGGER immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_log
          FOR EACH STATEMENT EXECUTE FUNCTION audit_guard();
        DO $migration$
        BEGIN
          RAISE NOTICE 'DROP TABLE is mentioned as a warning only';
        END
        $migration$;
        ALTER TABLE audit_log DROP CONSTRAINT audit_log_state_check;
        UPDATE audit_log SET state='ready' WHERE id=1;
        """
        self.assertEqual(dangerous_sql_reasons(sql), [])


class MigrationCheckerIntegrationTest(unittest.TestCase):
    def repository(self, sql="CREATE TABLE customers (id BIGINT PRIMARY KEY);\n"):
        temporary = tempfile.TemporaryDirectory()
        repo = Path(temporary.name)
        subprocess.run(["git", "init", "-b", "main"], cwd=repo, check=True, capture_output=True)
        subprocess.run(["git", "config", "user.name", "CI Test"], cwd=repo, check=True)
        subprocess.run(["git", "config", "user.email", "ci-test@example.invalid"], cwd=repo, check=True)
        (repo / "migrations").mkdir()
        (repo / "migrations/0001_initial.sql").write_text(sql, encoding="utf-8")
        subprocess.run(["git", "add", "migrations"], cwd=repo, check=True)
        subprocess.run(["git", "commit", "-m", "base migration"], cwd=repo, check=True, capture_output=True)
        base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
        return temporary, repo, base

    def run_checker(self, repo, base):
        return subprocess.run([sys.executable, str(CHECKER), "--base", base], cwd=repo,
                              text=True, capture_output=True)

    def test_changed_safe_migration_passes(self):
        temporary, repo, base = self.repository()
        with temporary:
            (repo / "migrations/0002_add_status.sql").write_text(
                "ALTER TABLE customers ADD COLUMN status TEXT;\n", encoding="utf-8")
            subprocess.run(["git", "add", "migrations"], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-m", "add status"], cwd=repo, check=True, capture_output=True)
            result = self.run_checker(repo, base)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("passed destructive SQL checks", result.stdout)

    def test_changed_destructive_migration_fails(self):
        temporary, repo, base = self.repository()
        with temporary:
            (repo / "migrations/0002_remove_customers.sql").write_text(
                "DROP TABLE customers;\n", encoding="utf-8")
            subprocess.run(["git", "add", "migrations"], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-m", "remove customers"], cwd=repo,
                           check=True, capture_output=True)
            result = self.run_checker(repo, base)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("destructive migration SQL is rejected", result.stderr)

    def test_deleting_historical_migration_fails(self):
        temporary, repo, base = self.repository()
        with temporary:
            subprocess.run(["git", "rm", "migrations/0001_initial.sql"], cwd=repo,
                           check=True, capture_output=True)
            subprocess.run(["git", "commit", "-m", "delete migration"], cwd=repo,
                           check=True, capture_output=True)
            result = self.run_checker(repo, base)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("migration history files are immutable", result.stderr)


if __name__ == "__main__":
    unittest.main()
