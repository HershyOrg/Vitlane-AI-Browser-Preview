DROP INDEX IF EXISTS idx_agency_order_processes_wake;
ALTER TABLE agency_order_processes
    DROP COLUMN IF EXISTS workflow,
    DROP COLUMN IF EXISTS wake_at,
    DROP COLUMN IF EXISTS last_applied_seq;

DROP INDEX IF EXISTS idx_order_process_commands_order;
DROP INDEX IF EXISTS idx_order_process_commands_due;
DROP TABLE IF EXISTS order_process_commands;

DROP INDEX IF EXISTS idx_order_process_events_unapplied;
DROP TABLE IF EXISTS order_process_events;
