-- Owner: referral. A verified inviter is required for invitation attribution;
-- team grouping is optional in both direct and invitation participation.
ALTER TABLE referral_participations DROP CONSTRAINT referral_participations_check;
ALTER TABLE referral_participations ADD CONSTRAINT referral_participations_check CHECK (
    (invitation_id IS NULL AND inviter_customer_id IS NULL AND inviter_team_id IS NULL)
    OR (invitation_id IS NOT NULL AND inviter_customer_id IS NOT NULL)
);
