-- Owner: internal/customer. Sidebar operational annotations remain local facts
-- and are deliberately separate from directory sync projection metadata.
CREATE TABLE customer_sidebar_profiles (
    customer_id BIGINT PRIMARY KEY REFERENCES customers(id) ON DELETE RESTRICT,
    profile_source TEXT NOT NULL DEFAULT '' CHECK (btrim(profile_source)=profile_source AND char_length(profile_source)<=200),
    industry TEXT NOT NULL DEFAULT '' CHECK (btrim(industry)=industry AND char_length(industry)<=200),
    industry_description TEXT NOT NULL DEFAULT '' CHECK (char_length(industry_description)<=2000),
    needs_blockers_followup TEXT NOT NULL DEFAULT '' CHECK (char_length(needs_blockers_followup)<=2000),
    version BIGINT NOT NULL CHECK (version>=1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
