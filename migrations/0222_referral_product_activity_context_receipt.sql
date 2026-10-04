-- Owner: Referral. Extend the existing operation receipt contract for
-- authenticated product activity context issuance. Forward-only: preserve
-- every prior receipt, result kind, unique key and append-only trigger.
ALTER TABLE referral_operation_receipts
    DROP CONSTRAINT referral_operation_receipts_operation_check;
ALTER TABLE referral_operation_receipts
    ADD CONSTRAINT referral_operation_receipts_operation_check CHECK (
        operation IN ('join','issue_invitation','campaign_create','campaign_update',
            'campaign_state','team_create','invite_reverse','invite_revoke',
            'reward_record','campaign_close','campaign_posters','issue_product_activity_context')
    );
