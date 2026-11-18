// Reusable business definitions. Published report contracts expose these meanings
// and dimensions; departmental database views continue to enforce data scope.
export const netRevenueMetric={key:'net_revenue',label:'净收入',unit:'currency',currency:'CNY',timezone:'Asia/Singapore',meaning:'订单实付减成功退款，退款回溯原支付日；金额单位为人民币元。'};
export function netRevenuePeriodSQL(view){
 if(!['reporting.finance_orders','reporting.management_fulfillment'].includes(view))throw new Error('未声明的指标数据集');
 return `WITH settings AS (SELECT :start_date::date AS starts,:end_date::date AS ends),
 windows AS (
  SELECT 1 AS ordinal,'本期'::text AS period_name,starts AS period_start,ends AS period_end FROM settings
  UNION ALL SELECT 2,'上一等长期间',starts-(ends-starts+1),starts-1 FROM settings
  UNION ALL SELECT 3,'去年同期',(starts-interval '1 year')::date,(ends-interval '1 year')::date FROM settings
 ), coverage AS (
  SELECT (SELECT min((paid_at AT TIME ZONE 'Asia/Singapore')::date)+1 FROM ${view} WHERE paid_at IS NOT NULL) AS coverage_start,
   (source_watermark AT TIME ZONE 'Asia/Singapore')::date-1 AS coverage_end,source_watermark AS data_as_of FROM reporting.dataset_status
 ), observed AS (
  SELECT w.*,c.*,coalesce(w.period_end>=w.period_start AND w.period_start>=c.coverage_start AND w.period_end<=c.coverage_end,false) AS covered,
   (SELECT coalesce(sum(net_revenue),0)::numeric(18,2) FROM ${view} WHERE paid_at IS NOT NULL
    AND (paid_at AT TIME ZONE 'Asia/Singapore')::date BETWEEN w.period_start AND w.period_end
    AND (:channel::text IS NULL OR channel=:channel::text) AND (:city::text IS NULL OR city=:city::text)) AS observed_revenue
  FROM windows w CROSS JOIN coverage c
 ), amounts AS (SELECT *,CASE WHEN covered THEN observed_revenue ELSE NULL END AS net_revenue FROM observed),
 current_period AS (SELECT net_revenue FROM amounts WHERE ordinal=1)
 SELECT a.period_name,a.period_start::text,a.period_end::text,a.covered,a.net_revenue,
  CASE WHEN a.ordinal>1 AND a.net_revenue IS NOT NULL AND c.net_revenue IS NOT NULL AND a.net_revenue<>0 THEN round(100*(c.net_revenue-a.net_revenue)/a.net_revenue,2) ELSE NULL END::numeric(18,2) AS change_percent,
  CASE WHEN a.period_end<a.period_start THEN '开始日期晚于结束日期'
   WHEN NOT a.covered THEN '样本未覆盖完整期间，不能比较'
   WHEN a.ordinal=1 THEN '本期；仅使用源水位前的完整自然日'
   WHEN c.net_revenue IS NULL THEN '本期数据不完整，不能比较'
   WHEN a.net_revenue=0 THEN '基期为0，不计算增长率'
   WHEN a.ordinal=2 THEN '本期较上一等长期间变动，百分比保留2位'
   ELSE '本期较去年同历日变动；闰日回退至2月28日' END AS comparison_note,
  a.coverage_start::text,a.coverage_end::text,a.data_as_of
 FROM amounts a CROSS JOIN current_period c ORDER BY a.ordinal`;
}
