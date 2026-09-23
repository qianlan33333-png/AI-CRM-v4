-- Complete Provider membership is independent from CRM root coverage.
-- No historical canonical snapshot is upgraded without a fresh Provider read.
CREATE TABLE wecom_group_provider_facts (
 corp_scope text NOT NULL, chat_reference text NOT NULL,
 observed_at timestamptz NOT NULL, complete boolean NOT NULL,
 identity_hashes text[] NOT NULL,
 PRIMARY KEY(corp_scope,chat_reference)
);
CREATE TABLE wecom_group_provider_observations (
 corp_scope text NOT NULL, chat_reference text NOT NULL,
 observed_at timestamptz NOT NULL, identity_hashes text[] NOT NULL,
 PRIMARY KEY(corp_scope,chat_reference,observed_at)
);
