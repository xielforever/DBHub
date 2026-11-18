ALTER TABLE sys_reports DROP CONSTRAINT sys_reports_chart_type_check;
ALTER TABLE sys_reports ADD CONSTRAINT sys_reports_chart_type_check
 CHECK(chart_type IN('table','bar','line','pie','metric','pivot','stacked_bar','dual_axis'));
