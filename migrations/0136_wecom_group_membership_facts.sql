-- Owner: wecom. Local complete Provider-read projection, not a send queue.
CREATE TABLE wecom_group_membership_facts (
 corp_scope text NOT NULL,
 chat_reference text NOT NULL,
 observed_at timestamptz NOT NULL,
 complete boolean NOT NULL,
 external_count integer NOT NULL CHECK(external_count >= 0),
 unresolved_count integer NOT NULL CHECK(unresolved_count >= 0 AND unresolved_count <= external_count),
 customer_ids bigint[] NOT NULL,
 failure_code text NOT NULL DEFAULT '',
 PRIMARY KEY(corp_scope,chat_reference),
 CHECK (NOT complete OR (unresolved_count=0 AND failure_code=''))
);
