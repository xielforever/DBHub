import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

// This is an explicit local fixture tool. Credentials travel over stdin, never
// command arguments or logs. Original business databases are read-only sources.
if(!process.argv.includes('--local'))throw new Error('仅供本机合成数据，请显式传入 --local');
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const stateDir=path.join(root,'.dbhub/simulation');fs.mkdirSync(stateDir,{recursive:true});
const credentialsFile=path.join(stateDir,'department-credentials.json');
const state=fs.existsSync(credentialsFile)?JSON.parse(fs.readFileSync(credentialsFile,'utf8')):{createdAt:new Date().toISOString(),database:'dbhub_analytics',databaseRoles:{},users:{}};
const quote=value=>"'"+String(value).replaceAll("'","''")+"'";
const identifier=value=>'"'+value.replaceAll('"','""')+'"';
function psql(database,input){try{return execFileSync('wsl.exe',['-d','Ubuntu-24.04','--','docker','exec','-i','dbhub-wsl-demo-postgres-1','psql','-X','-q','-A','-t','-U','demo_admin','-d',database,'-v','ON_ERROR_STOP=1'],{input,encoding:null,maxBuffer:40*1024*1024,stdio:['pipe','pipe','pipe']});}catch(e){let message=(e.stderr||e.message).toString();for(const role of Object.values(state.databaseRoles))message=message.replaceAll(role.password,'[REDACTED]');throw new Error('analytics SQL failed: '+message.slice(-3000));}}
if(psql('dbhub_demo',"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='dbhub_analytics')").toString().trim()!=='t')psql('dbhub_demo','CREATE DATABASE dbhub_analytics OWNER demo_admin');
psql(state.database,fs.readFileSync(path.join(root,'scripts/demo/analytics.sql')));
const inputs=[
 ['dbhub_demo','customers',`SELECT u.id,u.user_no,u.display_name,'TEST-***-'||right(u.phone,4) AS phone_masked,c.city,c.region,u.channel,u.registered_at,u.last_login_at,m.member_no,l.name AS level_name,m.points_balance,m.lifetime_spend,u.status FROM mall.users u JOIN mall.cities c ON c.id=u.city_id LEFT JOIN mall.members m ON m.user_id=u.id LEFT JOIN mall.member_levels l ON l.id=m.level_id ORDER BY u.id`],
 ['dbhub_demo','orders',`SELECT d.id,d.order_no,d.user_id,d.city,d.province,d.region,d.channel,d.status,d.total_amount,d.discount_amount,d.shipping_fee,d.payable_amount,d.created_at,d.paid_at,d.completed_at,d.refund_amount,d.net_revenue,o.user_coupon_id FROM mall.v_order_detail d JOIN mall.orders o ON o.id=d.id ORDER BY d.id`],
 ['tms','shipments',`SELECT t.id,t.shipment_no,t.source_order_id,c.name,t.recipient_city,t.recipient_region,t.status,t.freight_amount,t.carrier_cost,t.created_at,t.shipped_at,t.expected_delivery_at,t.delivered_at,t.returned_at FROM public.shipments t JOIN public.carriers c ON c.id=t.carrier_id ORDER BY t.id`],
 ['wms','outbounds',`SELECT o.id,o.outbound_no,o.source_order_id,w.id,w.name,w.city,o.status,o.shipment_no,o.total_quantity,o.created_at,o.picked_at,o.shipped_at FROM public.outbound_orders o JOIN public.warehouses w ON w.id=o.warehouse_id ORDER BY o.id`],
 ['wms','inventory',`SELECT i.id,i.warehouse_id,w.name,w.city,p.sku,p.name,p.category,i.on_hand_qty,i.reserved_qty,i.available_qty,p.safety_stock,p.cost_price,i.updated_at FROM public.inventory_balances i JOIN public.products p ON p.id=i.product_id JOIN public.warehouses w ON w.id=i.warehouse_id ORDER BY i.id`],
 ['dbhub_demo','coupons',`SELECT c.id,c.user_id,t.campaign,t.name,c.status,c.issued_at,c.expires_at,c.used_at FROM mall.user_coupons c JOIN mall.coupon_templates t ON t.id=c.template_id ORDER BY c.id`],
 ['tms','tracking',`SELECT id,shipment_id,sequence_no,event_type,event_time,location,description FROM public.tracking_events ORDER BY id`],
];
const sourceWatermarks={};
for(const [db,sql] of [['dbhub_demo','SELECT max(created_at) FROM mall.users'],['tms','SELECT max(created_at) FROM public._seed_runs'],['wms','SELECT max(updated_at) FROM public.inventory_balances']])sourceWatermarks[db]=psql(db,sql).toString().trim();
if(Object.values(sourceWatermarks).some(value=>!value||!Number.isFinite(Date.parse(value))))throw new Error('Source watermark missing');
const chunks=[Buffer.from('BEGIN;\nSET LOCAL statement_timeout=\'60s\';\nTRUNCATE bi_private.tracking,bi_private.coupons,bi_private.inventory,bi_private.outbounds,bi_private.shipments,bi_private.orders,bi_private.customers;\n')];
for(const [db,table,query] of inputs){const csv=psql(db,`BEGIN READ ONLY; COPY (${query}) TO STDOUT WITH (FORMAT csv, FORCE_QUOTE *); COMMIT;`);chunks.push(Buffer.from(`COPY bi_private.${identifier(table)} FROM STDIN WITH (FORMAT csv);\n`),csv,Buffer.from('\\.\n'));}
const batch=crypto.randomUUID();
const countsSql="jsonb_build_object("+inputs.map(([,table])=>`${quote(table)},(SELECT count(*) FROM bi_private.${identifier(table)})`).join(',')+")";
chunks.push(Buffer.from(`INSERT INTO bi_private.dataset_state(id,extracted_at,source_watermark,source_watermarks,row_counts,batch_id) VALUES(1,now(),${quote(new Date(Math.min(...Object.values(sourceWatermarks).map(Date.parse))).toISOString())}::timestamptz,${quote(JSON.stringify(sourceWatermarks))}::jsonb,${countsSql},${quote(batch)}) ON CONFLICT(id) DO UPDATE SET extracted_at=excluded.extracted_at,source_watermark=excluded.source_watermark,source_watermarks=excluded.source_watermarks,row_counts=excluded.row_counts,batch_id=excluded.batch_id;\nCOMMIT;\nANALYZE;\n`));
psql(state.database,Buffer.concat(chunks));
const grants={
 marketing:['marketing_members','sales_daily','coupon_performance'],
 sales:['sales_fulfillment','sales_members'],
 transport:['transport_shipments','transport_tracking'],
 warehouse:['warehouse_stock','warehouse_outbounds'],
 finance:['finance_orders','sales_daily'],
 management:['management_fulfillment','sales_daily'],
 developer:['marketing_members','sales_fulfillment','sales_members','transport_shipments','transport_tracking','warehouse_stock','warehouse_outbounds','finance_orders','sales_daily','coupon_performance','management_fulfillment'],
};
for(const [department,views] of Object.entries(grants)){
 const role=state.databaseRoles[department]||{username:'dbhub_demo_'+department,password:crypto.randomBytes(24).toString('base64url')};state.databaseRoles[department]=role;
 fs.writeFileSync(credentialsFile,JSON.stringify(state,null,2),{mode:0o600});
 const exists=psql(state.database,`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=${quote(role.username)})`).toString().trim()==='t';
 if(!exists)psql(state.database,`CREATE ROLE ${identifier(role.username)} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD ${quote(role.password)}; COMMENT ON ROLE ${identifier(role.username)} IS 'DBHub local synthetic department fixture';`);
 else if(psql(state.database,`SELECT COALESCE(shobj_description(oid,'pg_authid'),'')='DBHub local synthetic department fixture' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole FROM pg_roles WHERE rolname=${quote(role.username)}`).toString().trim()!=='t')throw new Error('Existing database role is not owned by this fixture: '+role.username);
 psql(state.database,`GRANT CONNECT ON DATABASE dbhub_analytics TO ${identifier(role.username)}; GRANT USAGE ON SCHEMA reporting TO ${identifier(role.username)}; GRANT SELECT ON ${[...views,'dataset_status'].map(view=>'reporting.'+identifier(view)).join(',')} TO ${identifier(role.username)};`);
}
psql(state.database,'REVOKE ALL ON DATABASE dbhub_analytics FROM PUBLIC;');
const summary=JSON.parse(psql(state.database,"SELECT json_build_object('batch_id',batch_id,'extracted_at',extracted_at,'source_watermark',source_watermark,'source_watermarks',source_watermarks,'row_counts',row_counts,'fulfillment_rows',(SELECT count(*) FROM reporting.management_fulfillment),'east_sales_rows',(SELECT count(*) FROM reporting.sales_fulfillment),'warehouse_rows',(SELECT count(*) FROM reporting.warehouse_stock)) FROM bi_private.dataset_state WHERE id=1").toString());
fs.writeFileSync(path.join(stateDir,'analytics-verification.json'),JSON.stringify(summary,null,2));
console.log(JSON.stringify({...summary,databaseRoles:Object.keys(grants)},null,2));
