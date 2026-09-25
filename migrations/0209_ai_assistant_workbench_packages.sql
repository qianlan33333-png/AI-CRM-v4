-- Owner: internal/aiassistant. UnionID never reaches this table.
CREATE TABLE ai_assistant_workbench_packages (
 plan_id BIGINT PRIMARY KEY REFERENCES ai_assistant_plans(id) ON DELETE RESTRICT,
 actor_ref TEXT NOT NULL,
 client_reference TEXT NOT NULL,
 audience_package_id TEXT NOT NULL,
 audience_version TEXT NOT NULL,
 copy_package_id TEXT NOT NULL,
 copy_version TEXT NOT NULL,
 strategy_version TEXT NOT NULL,
 product_fact_version TEXT NOT NULL,
 source_fingerprint TEXT NOT NULL CHECK (source_fingerprint ~ '^sha256:[0-9a-f]{64}$'),
 approval_revision TEXT NOT NULL,
 member_count INTEGER NOT NULL CHECK (member_count BETWEEN 1 AND 10),
 UNIQUE(actor_ref,client_reference)
);
