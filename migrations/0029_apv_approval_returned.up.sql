-- 0029 module / approval workflow status — a name for a returned chain
--
-- "Return for correction" is one of the four decisions an approver can take and it is not a
-- cancellation: a returned pass goes back to the person who filed it, to be fixed and filed
-- again, while a cancelled chain is a pass nobody is waiting on. Both were being written as
-- 'cancelled', so a queue screen could not tell them apart without opening the audit log.
--
-- Re-runnable: the constraint is dropped before it is added, which is how MariaDB makes an
-- ADD CONSTRAINT idempotent.

ALTER TABLE approval_requests
    DROP CONSTRAINT IF EXISTS chk_approval_requests_status;

ALTER TABLE approval_requests
    ADD CONSTRAINT chk_approval_requests_status CHECK (status IN
        ('not_required', 'pending', 'in_progress', 'approved', 'rejected', 'returned',
         'cancelled', 'expired', 'escalated'));

ALTER TABLE approval_request_steps
    DROP CONSTRAINT IF EXISTS chk_approval_request_steps_status;

ALTER TABLE approval_request_steps
    ADD CONSTRAINT chk_approval_request_steps_status CHECK (status IN
        ('pending', 'approved', 'rejected', 'returned', 'skipped', 'delegated', 'expired',
         'recalled', 'auto_approved'));
