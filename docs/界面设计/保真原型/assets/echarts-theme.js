/* ECharts 全局中文字体补丁：headless/极简系统无 CJK 字体时，
   canvas 内中文会回退到 sans-serif 缺字方块；统一注入 Noto Sans SC。
   生产前端由全局 ECharts 主题承担同等职责，原型用补丁模拟。 */
(function () {
  if (!window.echarts) return;
  var FONT = "'Noto Sans SC', -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif";
  var origInit = echarts.init;
  echarts.init = function () {
    var chart = origInit.apply(this, arguments);
    var origSetOption = chart.setOption;
    chart.setOption = function (option, notMerge, lazyUpdate) {
      if (option && typeof option === 'object' && !option.__fontPatched) {
        option.textStyle = Object.assign({ fontFamily: FONT }, option.textStyle);
        option.__fontPatched = true;
      }
      return origSetOption.call(this, option, notMerge, lazyUpdate);
    };
    return chart;
  };
})();
