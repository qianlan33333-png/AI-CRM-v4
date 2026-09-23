-- Owner: internal/groupops. WeCom permits unnamed groups; retain the absent
-- name as an empty value instead of rejecting the complete directory snapshot.
ALTER TABLE group_ops_directory_groups
    DROP CONSTRAINT group_ops_directory_groups_display_name_check,
    ADD CONSTRAINT group_ops_directory_groups_display_name_check
        CHECK (btrim(display_name) = display_name AND char_length(display_name) BETWEEN 0 AND 128);
