-- 0029 module / approval workflow status
-- Only the two vocabularies. Chains already written as 'returned' would fail the older
-- check, so rolling this back means deciding what to do with them, not just the constraint.

ALTER TABLE approval_requests
    DROP CONSTRAINT IF EXISTS chk_approval_requests_status;

ALTER TABLE approval_requests
    ADD CONSTRAINT chk_approval_requests_status CHECK (status IN
        ('not_required', 'pending', 'in_progress', 'approved', 'rejected', 'cancelled',
         'expired', 'escalated'));

ALTER TABLE approval_request_steps
    DROP CONSTRAINT IF EXISTS chk_approval_request_steps_status;

ALTER TABLE approval_request_steps
    ADD CONSTRAINT chk_approval_request_steps_status CHECK (status IN
        ('pending', 'approved', 'rejected', 'skipped', 'delegated', 'expired', 'recalled',
         'auto_approved'));
