-- Owner: internal/survey.
-- Forward-only: correct the PostgreSQL regular expression used by the
-- existing OAuth-state redirect constraint. The 0018 literal contained two
-- backslashes under standard_conforming_strings, so valid Host redirects such
-- as /h5/all.html?slug=survey could not be persisted.

ALTER TABLE survey_oauth_states
    DROP CONSTRAINT survey_oauth_states_redirect;

ALTER TABLE survey_oauth_states
    ADD CONSTRAINT survey_oauth_states_redirect
    CHECK (redirect_path ~ '^/h5/(all|one)\.html\?slug=[a-z0-9][a-z0-9-]{0,127}$');
