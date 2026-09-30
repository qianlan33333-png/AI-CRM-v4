-- Owner: media. Existing plans retain their mode and stable public/provider IDs.
ALTER TABLE media_invitation_plans ADD COLUMN native_options JSONB;
ALTER TABLE media_invitation_join_ways ADD COLUMN native_options JSONB;
ALTER TABLE media_invitation_plans DROP CONSTRAINT media_invitation_plans_mode_check;
ALTER TABLE media_invitation_plans DROP CONSTRAINT media_invitation_plans_check;
ALTER TABLE media_invitation_plans ADD CONSTRAINT media_invitation_plans_mode_check CHECK(mode IN ('single','sequence','native'));
ALTER TABLE media_invitation_plans ADD CONSTRAINT media_invitation_plans_check CHECK(
 (mode='single' AND threshold IS NULL AND native_options IS NULL) OR
 (mode='sequence' AND threshold BETWEEN 1 AND 200 AND threshold IS NOT NULL AND native_options IS NULL) OR
 (mode='native' AND threshold IS NULL AND native_options IS NOT NULL AND jsonb_typeof(native_options)='object')
);
ALTER TABLE media_invitation_join_ways ADD CHECK(native_options IS NULL OR jsonb_typeof(native_options)='object');
