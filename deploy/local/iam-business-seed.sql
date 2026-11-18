\getenv reader_password IAM_READER_PASSWORD
\getenv writer_password IAM_WRITER_PASSWORD
BEGIN;
DO $$BEGIN IF current_database()<>'iam_test' OR current_user<>'iam_test' THEN RAISE EXCEPTION 'isolated fixture only';END IF;END$$;
CREATE SCHEMA IF NOT EXISTS iam_business;
SELECT format('CREATE ROLE iam_acceptance_reader LOGIN PASSWORD %L',:'reader_password') WHERE NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='iam_acceptance_reader') \gexec
SELECT format('CREATE ROLE iam_acceptance_writer LOGIN PASSWORD %L',:'writer_password') WHERE NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='iam_acceptance_writer') \gexec
CREATE TABLE IF NOT EXISTS iam_business.members(id BIGINT PRIMARY KEY,name TEXT NOT NULL,department TEXT NOT NULL,tier TEXT NOT NULL,joined_at DATE NOT NULL,points INT NOT NULL);
CREATE TABLE IF NOT EXISTS iam_business.orders(id BIGINT PRIMARY KEY,member_id BIGINT NOT NULL REFERENCES iam_business.members(id),department TEXT NOT NULL,amount NUMERIC(12,2) NOT NULL,status TEXT NOT NULL,ordered_at DATE NOT NULL);
CREATE TABLE IF NOT EXISTS iam_business.coupons(id BIGINT PRIMARY KEY,member_id BIGINT NOT NULL REFERENCES iam_business.members(id),amount NUMERIC(12,2) NOT NULL,status TEXT NOT NULL,expires_at DATE NOT NULL);
CREATE TABLE IF NOT EXISTS iam_business.wms_stock(id BIGINT PRIMARY KEY,warehouse TEXT NOT NULL,sku TEXT NOT NULL,available_qty INT NOT NULL,reserved_qty INT NOT NULL);
CREATE TABLE IF NOT EXISTS iam_business.tms_shipments(id BIGINT PRIMARY KEY,order_id BIGINT NOT NULL REFERENCES iam_business.orders(id),route TEXT NOT NULL,carrier TEXT NOT NULL,status TEXT NOT NULL,freight NUMERIC(12,2) NOT NULL);
INSERT INTO iam_business.members SELECT i,'模拟会员'||lpad(i::text,4,'0'),(ARRAY['华东门店','华南门店','线上业务'])[1+i%3],(ARRAY['普通','银卡','金卡'])[1+i%3],current_date-(i%180),i*13%10000 FROM generate_series(1,300)i ON CONFLICT DO NOTHING;
INSERT INTO iam_business.orders SELECT i,1+i%300,(ARRAY['华东门店','华南门店','线上业务'])[1+i%3],50+(i*173%200000)/100.0,(ARRAY['已完成','待发货','运输中','已退款'])[1+i%4],current_date-(i%60) FROM generate_series(1,2400)i ON CONFLICT DO NOTHING;
INSERT INTO iam_business.coupons SELECT i,1+i%300,(ARRAY[10,20,50,100])[1+i%4],(ARRAY['可用','已核销','已过期'])[1+i%3],current_date+30-(i%60) FROM generate_series(1,900)i ON CONFLICT DO NOTHING;
INSERT INTO iam_business.wms_stock SELECT i,(ARRAY['上海仓','广州仓','成都仓'])[1+i%3],'SKU-'||lpad((1+i%60)::text,4,'0'),i*31%1000,i*7%100 FROM generate_series(1,180)i ON CONFLICT DO NOTHING;
INSERT INTO iam_business.tms_shipments SELECT i,i,(ARRAY['华东干线','华南干线','西南干线'])[1+i%3],(ARRAY['模拟承运A','模拟承运B'])[1+i%2],(ARRAY['待揽收','在途','已签收','异常'])[1+i%4],10+(i*37%10000)/100.0 FROM generate_series(1,1800)i ON CONFLICT DO NOTHING;
CREATE OR REPLACE VIEW iam_business.member_overview AS SELECT m.department,count(DISTINCT m.id)::BIGINT AS members,count(o.id)::BIGINT AS orders,COALESCE(sum(o.amount),0)::NUMERIC(16,2) AS sales,now() AS observed_at FROM iam_business.members m LEFT JOIN iam_business.orders o ON o.member_id=m.id GROUP BY m.department;
CREATE OR REPLACE VIEW iam_business.wms_overview AS SELECT warehouse,sum(available_qty)::BIGINT AS available,sum(reserved_qty)::BIGINT AS reserved,now() AS observed_at FROM iam_business.wms_stock GROUP BY warehouse;
CREATE OR REPLACE VIEW iam_business.tms_overview AS SELECT route,count(*)::BIGINT AS shipments,count(*) FILTER(WHERE status='已签收')::BIGINT AS delivered,sum(freight)::NUMERIC(16,2) AS freight,now() AS observed_at FROM iam_business.tms_shipments GROUP BY route;
GRANT USAGE ON SCHEMA iam_business TO iam_acceptance_reader,iam_acceptance_writer;
GRANT SELECT ON ALL TABLES IN SCHEMA iam_business TO iam_acceptance_reader;
GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA iam_business TO iam_acceptance_writer;
COMMIT;
