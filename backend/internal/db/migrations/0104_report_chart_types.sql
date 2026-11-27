ALTER TABLE sys_reports
    DROP CONSTRAINT sys_reports_chart_type_check,
    ADD CONSTRAINT sys_reports_chart_type_check CHECK (chart_type IN (
        'table', 'bar', 'line', 'pie', 'metric', 'pivot', 'stacked_bar', 'dual_axis',
        'horizontal_bar', 'area', 'scatter', 'histogram', 'funnel', 'heatmap'
    ));
