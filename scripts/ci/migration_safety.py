"""Static guard for destructive SQL in newly changed migrations.

This intentionally catches common irreversible statements, not every possible
form of SQL incompatibility. Migration review still needs to confirm that an
expand/backfill/contract sequence remains safe for the currently deployed app.
"""
from __future__ import annotations

import re


_FUNCTION_BODY = re.compile(
    r"(?is)(CREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE)\b.*?\bAS\s+)"
    r"(\$[A-Za-z_][A-Za-z_0-9]*\$|\$\$).*?\2"
)
_DOLLAR_QUOTE = re.compile(r"\$[A-Za-z_][A-Za-z_0-9]*\$|\$\$")

_DANGEROUS_PATTERNS = (
    (re.compile(r"\bDROP\s+(?:TABLE|VIEW|MATERIALIZED\s+VIEW|SCHEMA|TYPE|DOMAIN|SEQUENCE)\b", re.I),
     "dropping a table, view, schema, type, domain, or sequence"),
    (re.compile(r"\bALTER\s+TABLE\b[^;]*?\bDROP\s+COLUMN\b", re.I | re.S),
     "dropping a column"),
    (re.compile(r"\bALTER\s+TABLE\b[^;]*?\bRENAME\s+(?:COLUMN|TO)\b", re.I | re.S),
     "renaming a table or column"),
    (re.compile(r"\bTRUNCATE\s+(?:TABLE\s+)?(?!ON\b)[A-Za-z_\"]", re.I),
     "truncating a table"),
    (re.compile(r"\bDELETE\s+FROM\b", re.I), "deleting rows"),
)
_UPDATE_STATEMENT = re.compile(
    r"\bUPDATE\s+(?:ONLY\s+)?[A-Za-z_\"][\w.\"]*\s+SET\b[^;]*(?:;|$)", re.I | re.S
)


def _mask_sql_comments_and_strings(sql: str) -> str:
    """Mask comments and ordinary string values while keeping SQL keywords."""
    sql = _FUNCTION_BODY.sub(lambda match: match.group(1) + " ", sql)
    output = []
    index = 0
    dollar_tag = None
    while index < len(sql):
        if sql.startswith("--", index):
            end = sql.find("\n", index + 2)
            end = len(sql) if end < 0 else end
            output.append(" " * (end - index))
            index = end
            continue
        if sql.startswith("/*", index):
            start = index
            depth = 1
            index += 2
            while index < len(sql) and depth:
                if sql.startswith("/*", index):
                    depth += 1
                    index += 2
                elif sql.startswith("*/", index):
                    depth -= 1
                    index += 2
                else:
                    index += 1
            output.append("".join("\n" if char == "\n" else " " for char in sql[start:index]))
            continue
        if sql[index] == "'":
            start = index
            index += 1
            while index < len(sql):
                if sql[index] == "\\" and index + 1 < len(sql):
                    index += 2
                elif sql.startswith("''", index):
                    index += 2
                elif sql[index] == "'":
                    index += 1
                    break
                else:
                    index += 1
            prior_code = "".join(output[-256:])
            dynamic_sql = (dollar_tag is not None
                           and re.search(r"\bEXECUTE\s+(?:format\s*\(\s*)?$", prior_code, re.I))
            if dynamic_sql:
                output.append(sql[start + 1:index - 1].replace("''", "'"))
            else:
                output.append("".join("\n" if char == "\n" else " " for char in sql[start:index]))
            continue
        if sql[index] == '"':
            # Keep quoted identifier content so DROP TABLE "name" remains visible.
            index += 1
            while index < len(sql):
                if sql.startswith('""', index):
                    output.append('"')
                    index += 2
                elif sql[index] == '"':
                    index += 1
                    break
                else:
                    output.append(sql[index])
                    index += 1
            continue
        if sql[index] == "$":
            match = _DOLLAR_QUOTE.match(sql, index)
            if match:
                if dollar_tag is None:
                    dollar_tag = match.group(0)
                    output.append(" ")
                    index = match.end()
                    continue
                if match.group(0) == dollar_tag:
                    dollar_tag = None
                    output.append(" ")
                    index = match.end()
                    continue
                output.append(sql[index])
                index += 1
                continue
        output.append(sql[index])
        index += 1
    return "".join(output)


def dangerous_sql_reasons(sql: str) -> list[str]:
    """Return reasons to reject common destructive statements in a migration."""
    masked = _mask_sql_comments_and_strings(sql)
    reasons = []
    for pattern, reason in _DANGEROUS_PATTERNS:
        if pattern.search(masked) and reason not in reasons:
            reasons.append(reason)
    if any(not re.search(r"\bWHERE\b", match.group(0), re.I)
           for match in _UPDATE_STATEMENT.finditer(masked)):
        reasons.append("an UPDATE without a WHERE clause")
    return reasons
