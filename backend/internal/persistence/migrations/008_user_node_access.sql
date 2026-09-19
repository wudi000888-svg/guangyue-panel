-- User documents may now contain node_group_ids. Older releases ignore this
-- restriction and could grant default or plan nodes again. Register the model
-- boundary even though the JSON field needs no physical column, so startup and
-- the updater reject code-only rollback to an incompatible release.
SELECT 1;
