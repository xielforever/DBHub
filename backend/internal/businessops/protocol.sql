-- Version 1 is installed once by the database owner. Application accounts get
-- EXECUTE on dispatch_v1 only; no table/key access or arbitrary write grants.
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;
CREATE SCHEMA dbhub_operation_sandbox;
CREATE SCHEMA dbhub_ops_api_v1;
REVOKE ALL ON SCHEMA dbhub_operation_sandbox,dbhub_ops_api_v1 FROM PUBLIC;
CREATE TABLE dbhub_operation_sandbox.installation(
 id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id), installation_id UUID NOT NULL,
 signing_key TEXT NOT NULL CHECK(length(signing_key)=64), protocol_sha256 TEXT NOT NULL
);
CREATE TABLE dbhub_operation_sandbox.members(
 workspace_id BIGINT NOT NULL,owner_app_user_id BIGINT NOT NULL,member_no TEXT NOT NULL,
 points INTEGER NOT NULL CHECK(points BETWEEN 0 AND 100000),version BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY(workspace_id,owner_app_user_id,member_no)
);
CREATE TABLE dbhub_operation_sandbox.stocks(
 workspace_id BIGINT NOT NULL,owner_app_user_id BIGINT NOT NULL,sku TEXT NOT NULL,
 on_hand INTEGER NOT NULL CHECK(on_hand>=0),reserved INTEGER NOT NULL CHECK(reserved>=0 AND reserved<=on_hand),
 version BIGINT NOT NULL DEFAULT 1,PRIMARY KEY(workspace_id,owner_app_user_id,sku)
);
CREATE TABLE dbhub_operation_sandbox.shipments(
 workspace_id BIGINT NOT NULL,owner_app_user_id BIGINT NOT NULL,shipment_no TEXT NOT NULL,
 stage INTEGER NOT NULL CHECK(stage BETWEEN 0 AND 3),version BIGINT NOT NULL DEFAULT 1,
 PRIMARY KEY(workspace_id,owner_app_user_id,shipment_no)
);
CREATE TABLE dbhub_operation_sandbox.coupons(
 operation_id UUID NOT NULL,sequence INTEGER NOT NULL,workspace_id BIGINT NOT NULL,
 owner_app_user_id BIGINT NOT NULL,member_no TEXT NOT NULL,expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(operation_id,sequence),FOREIGN KEY(workspace_id,owner_app_user_id,member_no)
 REFERENCES dbhub_operation_sandbox.members(workspace_id,owner_app_user_id,member_no)
);
CREATE TABLE dbhub_operation_sandbox.receipts(
 operation_id UUID PRIMARY KEY,request_sha256 TEXT NOT NULL,workspace_id BIGINT NOT NULL,
 owner_app_user_id BIGINT NOT NULL,payload JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION dbhub_operation_sandbox.state_v1(p JSONB,lock_row BOOLEAN) RETURNS JSONB
LANGUAGE plpgsql SET search_path=pg_catalog,dbhub_operation_sandbox,pg_temp AS $$
DECLARE w BIGINT:=(p->>'workspace_id')::bigint; u BIGINT:=(p->>'actor_id')::bigint;
 k TEXT:=p->>'target_key'; kind TEXT:=p->>'kind'; amount INTEGER:=(p->>'amount')::integer;
 b JSONB; a JSONB; v BIGINT; x INTEGER; y INTEGER; stamp BIGINT:=(p->>'preview_at')::bigint;
BEGIN
 IF w<=0 OR u<=0 OR k!~'^[A-Za-z0-9_-]{1,80}$' OR kind NOT IN('member_points_adjust','member_coupon_issue','warehouse_stock_adjust','transport_status_advance') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='invalid operation';
 END IF;
 IF kind IN('member_points_adjust','member_coupon_issue') THEN
  IF lock_row THEN PERFORM 1 FROM dbhub_operation_sandbox.members WHERE workspace_id=w AND owner_app_user_id=u AND member_no=k FOR UPDATE; END IF;
  SELECT points,version INTO x,v FROM dbhub_operation_sandbox.members WHERE workspace_id=w AND owner_app_user_id=u AND member_no=k;
  IF v IS NULL THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='owned target unavailable'; END IF;
  IF kind='member_points_adjust' THEN
   IF amount=0 OR amount NOT BETWEEN -500 AND 500 OR x+amount NOT BETWEEN 0 AND 100000 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='points boundary'; END IF;
   b:=jsonb_build_object('member_no',k,'points',x,'version',v::text);
   a:=jsonb_build_object('member_no',k,'points',x+amount,'version',(v+1)::text);
  ELSE
   IF amount NOT BETWEEN 1 AND 10 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='coupon boundary'; END IF;
   SELECT count(*)::integer INTO y FROM dbhub_operation_sandbox.coupons WHERE workspace_id=w AND owner_app_user_id=u AND member_no=k;
   b:=jsonb_build_object('member_no',k,'issued_coupons',y,'version',v::text);
   a:=jsonb_build_object('member_no',k,'issued_coupons',y+amount,'new_coupon_expiry',to_char(to_timestamp(stamp)+interval '30 days','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'version',(v+1)::text);
  END IF;
 ELSIF kind='warehouse_stock_adjust' THEN
  IF lock_row THEN PERFORM 1 FROM dbhub_operation_sandbox.stocks WHERE workspace_id=w AND owner_app_user_id=u AND sku=k FOR UPDATE; END IF;
  SELECT on_hand,reserved,version INTO x,y,v FROM dbhub_operation_sandbox.stocks WHERE workspace_id=w AND owner_app_user_id=u AND sku=k;
  IF v IS NULL OR amount=0 OR amount NOT BETWEEN -500 AND 500 OR x+amount<y OR x+amount>100000 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='stock boundary or owned target unavailable'; END IF;
  b:=jsonb_build_object('sku',k,'on_hand',x,'reserved',y,'version',v::text);
  a:=jsonb_build_object('sku',k,'on_hand',x+amount,'reserved',y,'version',(v+1)::text);
 ELSE
  IF lock_row THEN PERFORM 1 FROM dbhub_operation_sandbox.shipments WHERE workspace_id=w AND owner_app_user_id=u AND shipment_no=k FOR UPDATE; END IF;
  SELECT stage,version INTO x,v FROM dbhub_operation_sandbox.shipments WHERE workspace_id=w AND owner_app_user_id=u AND shipment_no=k;
  IF v IS NULL OR amount<>1 OR x>=3 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='shipment transition or owned target unavailable'; END IF;
  b:=jsonb_build_object('shipment_no',k,'stage',(ARRAY['planned','picked_up','in_transit','delivered'])[x+1],'version',v::text);
  a:=jsonb_build_object('shipment_no',k,'stage',(ARRAY['planned','picked_up','in_transit','delivered'])[x+2],'version',(v+1)::text);
 END IF;
 RETURN jsonb_build_object('before',b,'after',a,'target_version',v::text,
 'before_sha256',encode(public.digest(b::text,'sha256'),'hex'),'after_sha256',encode(public.digest(a::text,'sha256'),'hex'));
END $$;
CREATE FUNCTION dbhub_ops_api_v1.dispatch_v1(raw_payload TEXT,signature TEXT) RETURNS TEXT
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,dbhub_operation_sandbox,pg_temp SET timezone='UTC' AS $$
DECLARE p JSONB; conf dbhub_operation_sandbox.installation%ROWTYPE; result JSONB; state JSONB;
 purpose TEXT; op UUID; prior dbhub_operation_sandbox.receipts%ROWTYPE; fingerprint TEXT; payload TEXT;
 w BIGINT; u BIGINT; k TEXT; amount INTEGER; kind TEXT; unix_now BIGINT:=floor(extract(epoch FROM clock_timestamp()));
BEGIN
 SELECT * INTO STRICT conf FROM dbhub_operation_sandbox.installation WHERE id;
 IF length(raw_payload)>12000 OR signature IS NULL OR signature<>encode(public.hmac('request:'||raw_payload,conf.signing_key,'sha256'),'hex') THEN
  RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='invalid operation proof';
 END IF;
 p:=raw_payload::jsonb; purpose:=p->>'purpose';
 IF (p->>'protocol')::integer<>1 OR (p->>'expires')::bigint<unix_now OR (p->>'expires')::bigint>unix_now+1900
 OR p->>'installation_id'<>conf.installation_id::text THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='expired or mismatched operation proof'; END IF;
 IF purpose='health' THEN
  result:=jsonb_build_object('installation_id',conf.installation_id,'nonce',p->>'nonce','protocol',1,
   'role_safe',NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=session_user AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls))
   AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles r ON r.oid=m.member WHERE r.rolname=session_user)
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.installation','SELECT,INSERT,UPDATE,DELETE')
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.members','SELECT,INSERT,UPDATE,DELETE')
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.stocks','SELECT,INSERT,UPDATE,DELETE')
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.shipments','SELECT,INSERT,UPDATE,DELETE')
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.coupons','SELECT,INSERT,UPDATE,DELETE')
   AND NOT has_table_privilege(session_user,'dbhub_operation_sandbox.receipts','SELECT,INSERT,UPDATE,DELETE'));
 ELSE
  w:=(p->>'workspace_id')::bigint; u:=(p->>'actor_id')::bigint; op:=NULLIF(p->>'operation_id','')::uuid;
  IF w<=0 OR u<=0 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='invalid actor'; END IF;
  IF purpose='lookup' THEN
   SELECT receipt.payload INTO result FROM dbhub_operation_sandbox.receipts receipt WHERE receipt.operation_id=op AND receipt.workspace_id=w AND receipt.owner_app_user_id=u;
   result:=jsonb_build_object('found',result IS NOT NULL,'receipt',result);
  ELSIF purpose IN('preview','apply') THEN
   IF (p->>'preview_at')::bigint>unix_now+60 OR (p->>'preview_at')::bigint<unix_now-2400 THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='preview time invalid'; END IF;
   IF purpose='apply' THEN
    IF op IS NULL OR (p->>'reviewer_id')::bigint<=0 OR (p->>'reviewer_id')::bigint=u THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='independent approval required'; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(op::text,0));
    -- Stable execution proof excludes its short transport deadline.
    fingerprint:=encode(public.digest((p-'expires')::text,'sha256'),'hex');
    SELECT * INTO prior FROM dbhub_operation_sandbox.receipts WHERE operation_id=op;
    IF FOUND THEN
     IF prior.request_sha256<>fingerprint THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='operation proof changed'; END IF;
     result:=prior.payload;
    END IF;
   END IF;
   IF result IS NULL THEN
    state:=dbhub_operation_sandbox.state_v1(p,purpose='apply');
    result:=state||jsonb_build_object('installation_id',conf.installation_id,'workspace_id',w,'actor_id',u,'kind',p->>'kind','target_key',p->>'target_key','amount',(p->>'amount')::integer,'preview_at',(p->>'preview_at')::bigint);
    IF purpose='apply' THEN
     IF state->>'before_sha256' IS DISTINCT FROM p->>'before_sha256' OR state->>'after_sha256' IS DISTINCT FROM p->>'after_sha256'
     OR state->>'target_version' IS DISTINCT FROM p->>'target_version' THEN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='target changed since approved preview'; END IF;
     k:=p->>'target_key';amount:=(p->>'amount')::integer;kind:=p->>'kind';
     IF kind='member_points_adjust' THEN UPDATE dbhub_operation_sandbox.members SET points=points+amount,version=version+1 WHERE workspace_id=w AND owner_app_user_id=u AND member_no=k;
     ELSIF kind='member_coupon_issue' THEN
      INSERT INTO dbhub_operation_sandbox.coupons SELECT op,n,w,u,k,to_timestamp((p->>'preview_at')::bigint)+interval '30 days' FROM generate_series(1,amount) n;
      UPDATE dbhub_operation_sandbox.members SET version=version+1 WHERE workspace_id=w AND owner_app_user_id=u AND member_no=k;
     ELSIF kind='warehouse_stock_adjust' THEN UPDATE dbhub_operation_sandbox.stocks SET on_hand=on_hand+amount,version=version+1 WHERE workspace_id=w AND owner_app_user_id=u AND sku=k;
     ELSE UPDATE dbhub_operation_sandbox.shipments SET stage=stage+1,version=version+1 WHERE workspace_id=w AND owner_app_user_id=u AND shipment_no=k; END IF;
     result:=result||jsonb_build_object('operation_id',op,'reviewer_id',(p->>'reviewer_id')::bigint,'executed_at',clock_timestamp(),'affected_rows',1);
     INSERT INTO dbhub_operation_sandbox.receipts VALUES(op,fingerprint,w,u,result,clock_timestamp());
    END IF;
   END IF;
  ELSE RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='unknown operation purpose'; END IF;
 END IF;
 payload:=result::text;
 RETURN jsonb_build_object('payload',payload,'signature',encode(public.hmac('response:'||payload,conf.signing_key,'sha256'),'hex'))::text;
END $$;
REVOKE ALL ON ALL TABLES IN SCHEMA dbhub_operation_sandbox FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA dbhub_operation_sandbox,dbhub_ops_api_v1 FROM PUBLIC;
