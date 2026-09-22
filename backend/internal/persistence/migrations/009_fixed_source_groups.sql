-- Source groups are fixed and per-group node selections use site-qualified
-- keys. Older code interprets these selections differently and may grant an
-- entire group. Prevent code-only rollback across this authorization boundary.
SELECT 1;
