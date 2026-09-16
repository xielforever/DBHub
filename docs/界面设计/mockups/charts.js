/* 保真原型用纯 SVG 图表（模拟 ECharts 现有风格：暗色网格 + 品牌色板） */
const PALETTE = ['#818cf8', '#a78bfa', '#34d399', '#fbbf24', '#f472b6', '#38bdf8', '#fb7185'];

function svgBarV(data, opt = {}) {
  const w = opt.w || 520, h = opt.h || 240, padL = 40, padR = 12, padT = 16, padB = 34;
  const iw = w - padL - padR, ih = h - padT - padB;
  const max = Math.max(...data.map(d => d.value)) * 1.15;
  const bw = iw / data.length * 0.52;
  const grid = [0, .25, .5, .75, 1].map(f => {
    const y = padT + ih - ih * f;
    return `<line x1="${padL}" y1="${y}" x2="${w - padR}" y2="${y}" stroke="rgba(255,255,255,.06)"/><text x="${padL - 8}" y="${y + 3}" text-anchor="end" font-size="9" fill="rgba(255,255,255,.35)">${Math.round(max * f)}</text>`;
  }).join('');
  const bars = data.map((d, i) => {
    const x = padL + iw / data.length * i + (iw / data.length - bw) / 2;
    const bh = ih * (d.value / max);
    const y = padT + ih - bh;
    const c = d.color || PALETTE[i % PALETTE.length];
    return `<rect x="${x}" y="${y}" width="${bw}" height="${bh}" rx="5" fill="${c}" opacity=".9"/>
      <text x="${x + bw / 2}" y="${y - 5}" text-anchor="middle" font-size="10" fill="rgba(255,255,255,.75)">${d.value}</text>
      <text x="${x + bw / 2}" y="${h - 12}" text-anchor="middle" font-size="10" fill="rgba(255,255,255,.55)">${d.label}</text>`;
  }).join('');
  return `<svg viewBox="0 0 ${w} ${h}" width="100%" height="${h}">${grid}${bars}</svg>`;
}

function svgLine(series, opt = {}) {
  const w = opt.w || 560, h = opt.h || 220, padL = 38, padR = 14, padT = 16, padB = 30;
  const iw = w - padL - padR, ih = h - padT - padB;
  const labels = opt.labels || [];
  const all = series.flatMap(s => s.data);
  const max = Math.max(...all) * 1.1, min = opt.zero ? 0 : Math.min(...all) * 0.9;
  const X = i => padL + (labels.length <= 1 ? iw / 2 : iw * i / (labels.length - 1));
  const Y = v => padT + ih - ih * (v - min) / (max - min);
  const grid = [0, .25, .5, .75, 1].map(f => {
    const y = padT + ih - ih * f;
    return `<line x1="${padL}" y1="${y}" x2="${w - padR}" y2="${y}" stroke="rgba(255,255,255,.06)"/><text x="${padL - 7}" y="${y + 3}" text-anchor="end" font-size="9" fill="rgba(255,255,255,.35)">${Math.round(min + (max - min) * f)}</text>`;
  }).join('');
  const xlab = labels.map((l, i) =>
    (i % Math.ceil(labels.length / 8) === 0) ? `<text x="${X(i)}" y="${h - 10}" text-anchor="middle" font-size="9" fill="rgba(255,255,255,.4)">${l}</text>` : '').join('');
  const lines = series.map((s, si) => {
    const c = s.color || PALETTE[si];
    const pts = s.data.map((v, i) => `${X(i)},${Y(v)}`).join(' ');
    const area = `<polyline points="${padL},${padT + ih} ${pts} ${X(s.data.length - 1)},${padT + ih}" fill="${c}" opacity=".12" stroke="none"/>`;
    const dots = s.data.map((v, i) => `<circle cx="${X(i)}" cy="${Y(v)}" r="2.6" fill="${c}"/>`).join('');
    return `${area}<polyline points="${pts}" fill="none" stroke="${c}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>${dots}`;
  }).join('');
  return `<svg viewBox="0 0 ${w} ${h}" width="100%" height="${h}">${grid}${lines}${xlab}</svg>`;
}

function svgDonut(segments, opt = {}) {
  const size = opt.size || 150, r = 54, ir = 34, cx = 70, cy = 70;
  const total = segments.reduce((s, x) => s + x.value, 0);
  let a0 = -Math.PI / 2;
  const arcs = segments.map((s, i) => {
    const a1 = a0 + s.value / total * Math.PI * 2;
    const large = a1 - a0 > Math.PI ? 1 : 0;
    const p = (r, a) => [cx + r * Math.cos(a), cy + r * Math.sin(a)];
    const [x0, y0] = p(r, a0), [x1, y1] = p(r, a1), [x2, y2] = p(ir, a1), [x3, y3] = p(ir, a0);
    const d = `M${x0} ${y0} A${r} ${r} 0 ${large} 1 ${x1} ${y1} L${x2} ${y2} A${ir} ${ir} 0 ${large} 0 ${x3} ${y3} Z`;
    a0 = a1;
    return `<path d="${d}" fill="${s.color || PALETTE[i % PALETTE.length]}"/>`;
  }).join('');
  return `<svg viewBox="0 0 ${size} ${size}" width="${size}" height="${size}">${arcs}<text x="${cx}" y="${cy - 3}" text-anchor="middle" font-size="17" font-weight="700" fill="#fff">${total}</text><text x="${cx}" y="${cy + 14}" text-anchor="middle" font-size="9" fill="rgba(255,255,255,.5)">${opt.unit || '总数'}</text></svg>`;
}

function svgSpark(points, color = '#818cf8', w = 120, h = 34) {
  const max = Math.max(...points), min = Math.min(...points);
  const X = i => 2 + (w - 4) * i / (points.length - 1);
  const Y = v => 3 + (h - 8) * (1 - (v - min) / (max - min || 1));
  const pts = points.map((v, i) => `${X(i)},${Y(v)}`).join(' ');
  return `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}"><polyline points="${pts}" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
}

function svgHBars(data, opt = {}) {
  const w = opt.w || 300, rowH = 30, padL = 8, barH = 12;
  const max = Math.max(...data.map(d => d.value));
  return `<svg viewBox="0 0 ${w} ${data.length * rowH + 6}" width="100%">` + data.map((d, i) => {
    const y = i * rowH + 8;
    const bw = (w - 130) * d.value / max;
    return `<text x="${padL}" y="${y + 11}" font-size="11" fill="rgba(255,255,255,.7)">${d.label}</text>
      <rect x="92" y="${y + 2}" width="${w - 130}" height="${barH}" rx="6" fill="rgba(255,255,255,.07)"/>
      <rect x="92" y="${y + 2}" width="${bw}" height="${barH}" rx="6" fill="${d.color || PALETTE[i % PALETTE.length]}" opacity=".85"/>
      <text x="${100 + bw}" y="${y + 12}" font-size="10" fill="rgba(255,255,255,.7)">${d.value}</text>`;
  }).join('') + '</svg>';
}
