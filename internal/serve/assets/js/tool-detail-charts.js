// tool-detail-charts.js — timeseries + input pattern charts for /tools/{name}.
window.kapmCharts.register('toolDetail', function () {
  const el = document.getElementById('tool-detail-data');
  if (!el) return;
  const data = JSON.parse(el.textContent);
  const ts = data.timeseries || [];
  const pats = data.patterns || [];
  const elTs = document.getElementById('chart-tool-timeseries');
  const elPat = document.getElementById('chart-tool-patterns');

  function getOrInit(dom) {
    return echarts.getInstanceByDom(dom) || echarts.init(dom, 'dark');
  }

  // Parse Go duration string ("81ms", "1.2s", "5m0s") to milliseconds.
  function durationMs(s) {
    if (typeof s === 'number') return s / 1e6;
    var m;
    if ((m = s.match(/^([\d.]+)ms$/))) return parseFloat(m[1]);
    if ((m = s.match(/^([\d.]+)s$/))) return parseFloat(m[1]) * 1000;
    if ((m = s.match(/^([\d.]+)m([\d.]+)s$/))) return parseFloat(m[1]) * 60000 + parseFloat(m[2]) * 1000;
    if ((m = s.match(/^([\d.]+)h/))) return parseFloat(m[1]) * 3600000;
    if ((m = s.match(/^([\d.]+)µs$/))) return parseFloat(m[1]) / 1000;
    return 0;
  }

  // Extract filename from a path-like string.
  function basename(s) {
    var i = s.lastIndexOf('/');
    return i >= 0 ? s.slice(i + 1) : s;
  }

  if (ts.length < 2) {
    if (elTs) {
      var prev = echarts.getInstanceByDom(elTs);
      if (prev) prev.dispose();
      elTs.innerHTML = '<div class="muted">insufficient data</div>';
    }
  } else {
    getOrInit(elTs).setOption({
      animation: false,
      backgroundColor: 'transparent',
      grid: { left: 56, right: 56, top: 16, bottom: 32 },
      xAxis: { type: 'time' },
      yAxis: [
        { type: 'value', name: 'ms', axisLabel: { formatter: function (v) { return v.toFixed(0); } } },
        { type: 'value', name: 'Errors' },
      ],
      series: [
        { type: 'line', name: 'Avg duration', data: ts.map(function (p) { return [p.bucket, durationMs(p.avgDuration)]; }) },
        { type: 'bar', name: 'Errors', yAxisIndex: 1, data: ts.map(function (p) { return [p.bucket, p.errorCount]; }) },
      ],
      tooltip: { trigger: 'axis', formatter: function (params) {
        var lines = [params[0].axisValueLabel];
        params.forEach(function (p) { lines.push(p.marker + ' ' + p.seriesName + ': ' + (p.seriesName === 'Avg duration' ? p.value[1].toFixed(0) + 'ms' : p.value[1])); });
        return lines.join('<br/>');
      }},
      legend: {},
    });
  }

  if (pats.length === 0) {
    if (elPat) {
      var prev2 = echarts.getInstanceByDom(elPat);
      if (prev2) prev2.dispose();
      elPat.innerHTML = '<div class="muted">No patterns</div>';
    }
  } else {
    var labels = pats.map(function (p) {
      var name = basename(p.summary);
      return name.length > 40 ? name.slice(0, 37) + '...' : name;
    }).reverse();
    getOrInit(elPat).setOption({
      animation: false,
      backgroundColor: 'transparent',
      grid: { left: 180, right: 16, top: 16, bottom: 24 },
      xAxis: { type: 'value' },
      yAxis: { type: 'category', data: labels },
      series: [{ type: 'bar', data: pats.map(function (p) { return p.count; }).reverse() }],
      tooltip: { formatter: function (p) {
        var full = pats[pats.length - 1 - p.dataIndex].summary;
        return full + '<br/>count: ' + p.value;
      }},
    });
  }
});
