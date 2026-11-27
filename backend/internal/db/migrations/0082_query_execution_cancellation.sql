ALTER TABLE sys_query_executions
    ADD COLUMN cancellation_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD CONSTRAINT query_execution_cancellation_terminal
        CHECK (NOT cancellation_confirmed OR status IN ('failed', 'rejected'));
