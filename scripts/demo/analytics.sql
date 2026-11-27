-- 仅用于已有合成业务库的本地演示；不改写商城、TMS 或 WMS 原始数据。
CREATE SCHEMA IF NOT EXISTS bi_private;
CREATE SCHEMA IF NOT EXISTS reporting;
REVOKE ALL ON SCHEMA bi_private FROM PUBLIC;
REVOKE ALL ON SCHEMA reporting FROM PUBLIC;
CREATE TABLE IF NOT EXISTS bi_private.dataset_state(
 id integer PRIMARY KEY CHECK(id=1), extracted_at timestamptz NOT NULL,
 source_watermark timestamptz NOT NULL, source_watermarks jsonb NOT NULL,
 row_counts jsonb NOT NULL, batch_id text NOT NULL
);
CREATE TABLE IF NOT EXISTS bi_private.customers(
 id bigint PRIMARY KEY, user_no text NOT NULL, display_name text NOT NULL,
 phone_masked text NOT NULL, city text NOT NULL, region text NOT NULL, channel text NOT NULL,
 registered_at timestamptz NOT NULL, last_login_at timestamptz, member_no text,
 level_name text, points_balance bigint, lifetime_spend numeric(14,2), status text NOT NULL
);
CREATE TABLE IF NOT EXISTS bi_private.orders(
 id bigint PRIMARY KEY, order_no text NOT NULL UNIQUE, user_id bigint NOT NULL REFERENCES bi_private.customers(id),
 city text NOT NULL, province text NOT NULL, region text NOT NULL, channel text NOT NULL, status text NOT NULL,
 total_amount numeric(14,2) NOT NULL, discount_amount numeric(14,2) NOT NULL,
 shipping_fee numeric(14,2) NOT NULL, payable_amount numeric(14,2) NOT NULL,
 created_at timestamptz NOT NULL, paid_at timestamptz, completed_at timestamptz,
 refund_amount numeric(14,2) NOT NULL, net_revenue numeric(14,2) NOT NULL, user_coupon_id bigint
);
CREATE INDEX IF NOT EXISTS orders_paid_date ON bi_private.orders(paid_at);
CREATE INDEX IF NOT EXISTS orders_customer ON bi_private.orders(user_id);
CREATE TABLE IF NOT EXISTS bi_private.shipments(
 id bigint PRIMARY KEY, shipment_no text NOT NULL UNIQUE, source_order_id bigint NOT NULL UNIQUE REFERENCES bi_private.orders(id),
 carrier_name text NOT NULL, recipient_city text NOT NULL, recipient_region text NOT NULL,
 status text NOT NULL, freight_amount numeric(14,2) NOT NULL, carrier_cost numeric(14,2) NOT NULL,
 created_at timestamptz NOT NULL, shipped_at timestamptz, expected_delivery_at timestamptz NOT NULL,
 delivered_at timestamptz, returned_at timestamptz
);
CREATE TABLE IF NOT EXISTS bi_private.outbounds(
 id bigint PRIMARY KEY, outbound_no text NOT NULL UNIQUE, source_order_id bigint NOT NULL UNIQUE REFERENCES bi_private.orders(id),
 warehouse_id bigint NOT NULL, warehouse_name text NOT NULL, warehouse_city text NOT NULL,
 status text NOT NULL, shipment_no text, total_quantity integer NOT NULL,
 created_at timestamptz NOT NULL, picked_at timestamptz, shipped_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbounds_warehouse ON bi_private.outbounds(warehouse_id);
CREATE TABLE IF NOT EXISTS bi_private.inventory(
 id bigint PRIMARY KEY, warehouse_id bigint NOT NULL, warehouse_name text NOT NULL, city text NOT NULL,
 sku text NOT NULL, product_name text NOT NULL, category text NOT NULL,
 on_hand_qty integer NOT NULL, reserved_qty integer NOT NULL, available_qty integer NOT NULL,
 safety_stock integer NOT NULL, cost_price numeric(12,2) NOT NULL, updated_at timestamptz NOT NULL,
 UNIQUE(warehouse_id,sku)
);
CREATE TABLE IF NOT EXISTS bi_private.coupons(
 id bigint PRIMARY KEY, user_id bigint NOT NULL REFERENCES bi_private.customers(id), campaign text NOT NULL,
 template_name text NOT NULL, status text NOT NULL, issued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL, used_at timestamptz
);
CREATE TABLE IF NOT EXISTS bi_private.tracking(
 id bigint PRIMARY KEY, shipment_id bigint NOT NULL REFERENCES bi_private.shipments(id), sequence_no integer NOT NULL,
 event_type text NOT NULL, event_time timestamptz NOT NULL, location text NOT NULL, description text NOT NULL
);
CREATE INDEX IF NOT EXISTS tracking_shipment ON bi_private.tracking(shipment_id,sequence_no);
CREATE OR REPLACE VIEW bi_private.fulfillment AS
SELECT o.*,c.user_no,c.display_name,c.phone_masked,c.member_no,c.level_name,
 w.outbound_no,w.warehouse_id,w.warehouse_name,w.status AS warehouse_status,w.total_quantity,
 w.picked_at,w.shipped_at AS warehouse_shipped_at,
 t.shipment_no,t.carrier_name,t.status AS transport_status,t.shipped_at,t.expected_delivery_at,t.delivered_at,
 CASE WHEN t.delivered_at IS NOT NULL THEN t.delivered_at<=t.expected_delivery_at END AS delivered_on_time,
 round(extract(epoch FROM(t.delivered_at-o.paid_at))/3600,2) AS fulfillment_hours,
 s.source_watermark AS data_as_of,s.extracted_at AS dataset_refreshed_at
FROM bi_private.orders o JOIN bi_private.customers c ON c.id=o.user_id
LEFT JOIN bi_private.outbounds w ON w.source_order_id=o.id
LEFT JOIN bi_private.shipments t ON t.source_order_id=o.id
CROSS JOIN bi_private.dataset_state s WHERE s.id=1;

CREATE OR REPLACE VIEW reporting.marketing_members AS
SELECT c.id AS user_id,c.user_no,c.city,c.region,c.channel,c.registered_at,c.last_login_at,
 c.member_no,c.level_name,c.points_balance,c.lifetime_spend,c.status,s.source_watermark AS data_as_of
FROM bi_private.customers c CROSS JOIN bi_private.dataset_state s WHERE s.id=1;
COMMENT ON VIEW reporting.marketing_members IS '会员与用户维度：不包含姓名、原始手机号及邮箱；金额为人民币元。';
CREATE OR REPLACE VIEW reporting.sales_fulfillment AS
SELECT order_no,user_no,display_name,phone_masked,city,region,channel,status,created_at,paid_at,
 payable_amount,refund_amount,net_revenue,member_no,outbound_no,warehouse_name,warehouse_status,
 shipment_no,carrier_name,transport_status,shipped_at,expected_delivery_at,delivered_at,
 fulfillment_hours,data_as_of
FROM bi_private.fulfillment WHERE region='华东';
COMMENT ON VIEW reporting.sales_fulfillment IS '华东销售客服限定视图：手机号已掩码；跨商城、出库及运单的一订单一行数据集。';
CREATE OR REPLACE VIEW reporting.sales_members AS
SELECT user_no,display_name,phone_masked,city,region,channel,member_no,level_name,points_balance,lifetime_spend,
 s.source_watermark AS data_as_of FROM bi_private.customers c CROSS JOIN bi_private.dataset_state s
WHERE c.region='华东' AND s.id=1;
CREATE OR REPLACE VIEW reporting.transport_shipments AS
SELECT t.*,o.order_no,s.source_watermark AS data_as_of
FROM bi_private.shipments t JOIN bi_private.orders o ON o.id=t.source_order_id CROSS JOIN bi_private.dataset_state s WHERE s.id=1;
CREATE OR REPLACE VIEW reporting.transport_tracking AS
SELECT t.shipment_no,e.sequence_no,e.event_type,e.event_time,e.location,e.description,s.source_watermark AS data_as_of
FROM bi_private.tracking e JOIN bi_private.shipments t ON t.id=e.shipment_id CROSS JOIN bi_private.dataset_state s WHERE s.id=1;
CREATE OR REPLACE VIEW reporting.warehouse_stock AS
SELECT i.*,round(i.on_hand_qty*i.cost_price,2) AS inventory_value,s.source_watermark AS data_as_of
FROM bi_private.inventory i CROSS JOIN bi_private.dataset_state s WHERE i.warehouse_id IN(1,2) AND s.id=1;
COMMENT ON VIEW reporting.warehouse_stock IS '仓储作业组仅能访问仓库1、2；前端筛选不能扩大此范围。';
CREATE OR REPLACE VIEW reporting.warehouse_outbounds AS
SELECT w.*,o.order_no,s.source_watermark AS data_as_of
FROM bi_private.outbounds w JOIN bi_private.orders o ON o.id=w.source_order_id CROSS JOIN bi_private.dataset_state s
WHERE w.warehouse_id IN(1,2) AND s.id=1;
CREATE OR REPLACE VIEW reporting.finance_orders AS
SELECT order_no,city,region,channel,status,created_at,paid_at,completed_at,payable_amount,discount_amount,
 refund_amount,net_revenue,data_as_of FROM bi_private.fulfillment;
CREATE OR REPLACE VIEW reporting.sales_daily AS
SELECT (paid_at AT TIME ZONE 'Asia/Singapore')::date AS sale_date,channel,city,region,
 count(*) AS paid_orders,count(DISTINCT user_id) AS paying_users,
 sum(payable_amount)::numeric(18,2) AS paid_amount,sum(refund_amount)::numeric(18,2) AS refund_amount,
 sum(net_revenue)::numeric(18,2) AS net_revenue,sum(discount_amount)::numeric(18,2) AS discount_amount,
 min(data_as_of) AS data_as_of FROM bi_private.fulfillment WHERE paid_at IS NOT NULL
GROUP BY 1,2,3,4;
COMMENT ON VIEW reporting.sales_daily IS '按新加坡支付日期归属；成功退款回溯原支付日，净收入=实付-成功退款；同比须有上年同期间数据。';
CREATE OR REPLACE VIEW reporting.coupon_performance AS
SELECT c.campaign,c.template_name,c.status,c.issued_at,c.used_at,o.order_no,o.payable_amount,o.discount_amount,
 o.refund_amount,o.net_revenue,s.source_watermark AS data_as_of
FROM bi_private.coupons c LEFT JOIN bi_private.orders o ON o.user_coupon_id=c.id CROSS JOIN bi_private.dataset_state s WHERE s.id=1;
COMMENT ON VIEW reporting.coupon_performance IS '发券和核销分析；没有活动成本和对照组时不宣称营销增量ROI。';
CREATE OR REPLACE VIEW reporting.management_fulfillment AS
SELECT order_no,city,region,channel,status,created_at,paid_at,completed_at,payable_amount,refund_amount,net_revenue,
 outbound_no,warehouse_id,warehouse_name,warehouse_status,shipment_no,carrier_name,transport_status,
 shipped_at,expected_delivery_at,delivered_at,delivered_on_time,fulfillment_hours,data_as_of,dataset_refreshed_at
FROM bi_private.fulfillment;
COMMENT ON VIEW reporting.management_fulfillment IS '统一履约分析：保留未出库或未发运订单，不暴露个人姓名、手机号或邮箱。';
CREATE OR REPLACE VIEW reporting.dataset_status AS SELECT batch_id,extracted_at,source_watermark,source_watermarks,row_counts FROM bi_private.dataset_state WHERE id=1;
REVOKE ALL ON ALL TABLES IN SCHEMA bi_private FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA reporting FROM PUBLIC;
