-- Owner: wecom. Immutable complete Provider observations for scheduled
-- evaluation at an earlier reference time. No jobs or old member import.
CREATE TABLE wecom_group_membership_observations (
 corp_scope text NOT NULL,
 chat_reference text NOT NULL,
 observed_at timestamptz NOT NULL,
 external_count integer NOT NULL CHECK(external_count>=0),
 customer_ids bigint[] NOT NULL,
 PRIMARY KEY(corp_scope,chat_reference,observed_at)
);
INSERT INTO wecom_group_membership_observations(corp_scope,chat_reference,observed_at,external_count,customer_ids)
SELECT corp_scope,chat_reference,observed_at,external_count,customer_ids FROM wecom_group_membership_facts WHERE complete AND unresolved_count=0;
