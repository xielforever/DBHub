\set ON_ERROR_STOP on
\i /diagnostic.sql
LOAD 'auto_explain';
SET client_min_messages=log;
SET auto_explain.log_min_duration=0;
SET auto_explain.log_analyze=on;
SET auto_explain.log_nested_statements=on;
SELECT dbhub_iam_has_action(1,'connection.use');
