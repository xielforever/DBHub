-- Keep the dependency filter ahead of the revocation lookup. Without the
-- materialization boundary a valid PostgreSQL plan may test every capability
-- before joining the two or three capabilities this operation actually needs.
DO $migration$ DECLARE ns TEXT:=current_schema();body TEXT;previous TEXT;
BEGIN
 SELECT p.prosrc INTO body FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=ns AND p.proname='dbhub_iam_has_action' AND p.pronargs=2;
 previous:=body;
 body:=replace(body,'), permitted AS MATERIALIZED (',
 '), candidate_caps AS MATERIALIZED (SELECT c.* FROM sys_iam_capabilities c WHERE c.action_id IN(SELECT id FROM needed) AND c.scope=''workspace''), permitted AS MATERIALIZED (');
 IF body=previous THEN RAISE EXCEPTION 'unexpected IAM dependency resolver';END IF;
 previous:=body;
 body:=replace(body,'SELECT c.action_id FROM sys_iam_capabilities c CROSS JOIN who w','SELECT c.action_id FROM candidate_caps c CROSS JOIN who w');
 IF body=previous THEN RAISE EXCEPTION 'unexpected IAM permission resolver';END IF;
 previous:=body;
 body:=replace(body,'WHERE c.action_id IN(SELECT id FROM needed) AND c.scope=''workspace'' AND w.qualified','WHERE w.qualified');
 IF body=previous THEN RAISE EXCEPTION 'unexpected IAM qualification resolver';END IF;
 EXECUTE format('CREATE OR REPLACE FUNCTION dbhub_iam_has_action(uid BIGINT,action TEXT) RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path=%I,pg_temp AS %L',ns,body);
END $migration$;
