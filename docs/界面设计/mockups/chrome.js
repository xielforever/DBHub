/* 应用外壳（侧边栏 + 顶栏），与生产 MainLayout 视觉一致；静态原型不可点导航 */
const NAV = [
  { title: '概览', items: [
    { key: 'dashboard', label: '仪表盘', icon: 'dashboard' },
  ]},
  { title: '数据', items: [
    { key: 'assets', label: '数据资产', icon: 'layers', isNew: true },
    { key: 'connections', label: '数据源管理', icon: 'database' },
    { key: 'query', label: 'SQL 工作台', icon: 'terminal' },
    { key: 'reports', label: '报表中心', icon: 'barChart', isNew: true },
  ]},
  { title: '安全', items: [
    { key: 'users', label: '用户与权限', icon: 'users' },
    { key: 'audit', label: '操作审计', icon: 'shield' },
  ]},
];

function renderChrome(cfg = {}) {
  const { active, title, search = 'plain', user = 'admin', role = 'A' } = cfg;
  const sidebar = document.getElementById('sidebar');
  const topbar = document.getElementById('topbar');

  sidebar.innerHTML = `
    <div class="brand">
      <span class="brand-logo">${ic('zap', '', 20)}</span>
      <div><div class="brand-name">DBHub</div><div class="brand-sub">数据管理平台</div></div>
    </div>
    <nav class="nav">
      ${NAV.map(g => `
        <div class="nav-group">
          <div class="nav-title">${g.title}</div>
          ${g.items.map(it => `
            <a class="nav-item ${active === it.key ? 'active' : ''}">
              ${ic(it.icon, '', 18)}<span>${it.label}</span>
              ${it.isNew ? '<span class="new">NEW</span>' : ''}
            </a>`).join('')}
        </div>`).join('')}
    </nav>
    <div class="rail-foot">
      <div class="health"><span class="dot"></span>后端在线</div>
    </div>`;

  let searchHtml = '';
  if (search !== 'hidden') {
    if (search === 'open') {
      searchHtml = `
        <div class="search-wrap" style="max-width:560px">
          <span class="anno" style="top:-10px;left:-11px">1</span>
          ${ic('search')}
          <input class="search-input" value="orders" style="border-color:rgba(102,126,234,.7);box-shadow:0 0 0 3px rgba(102,126,234,.18)" />
          <div class="panel search-pop">
            <div class="search-group">表 / 视图 <span class="muted" style="margin-left:auto;font-size:11px">4</span></div>
            <a class="search-item">${ic('table','',15)}<div><b>orders</b><span>订单核心库 · postgres · shop</span></div><span class="chip chip-amber">敏感</span></a>
            <a class="search-item">${ic('table','',15)}<div><b>order_items</b><span>订单核心库 · postgres · shop</span></div></a>
            <a class="search-item">${ic('eye','',15)}<div><b>v_orders_today</b><span>订单核心库 · postgres · shop（视图）</span></div></a>
            <a class="search-item">${ic('table','',15)}<div><b>orders_archive_2025</b><span>归档库 · mysql · ops</span></div></a>
            <div class="search-group">字段 <span class="muted" style="margin-left:auto;font-size:11px">2</span></div>
            <a class="search-item">${ic('columns','',15)}<div><b>orders</b>.<b style="color:#a5b4fc">order_no</b><span>订单编号 · varchar(32)</span></div></a>
            <a class="search-item">${ic('columns','',15)}<div><b>order_items</b>.<b style="color:#a5b4fc">qty</b><span>购买数量 · integer</span></div></a>
            <div class="search-group">报表 <span class="muted" style="margin-left:auto;font-size:11px">2</span></div>
            <a class="search-item">${ic('fileChart','',15)}<div><b>订单状态分布（近14天）</b><span>dev1 · 共享报表</span></div></a>
            <a class="search-item">${ic('fileChart','',15)}<div><b>每日 GMV 趋势</b><span>admin · 我的报表</span></div></a>
            <div class="search-group">查询历史 <span class="muted" style="margin-left:auto;font-size:11px">3</span></div>
            <a class="search-item mono" style="font-size:11.5px">${ic('history','',14)}<div><span>SELECT status, COUNT(*) FROM shop.<b>orders</b> GROUP…</span></div></a>
          </div>
        </div>`;
    } else {
      searchHtml = `
        <div class="search-wrap">
          ${ic('search')}
          <input class="search-input" placeholder="搜索数据源、表、字段、报表、查询历史…" />
        </div>`;
    }
  }

  topbar.innerHTML = `
    <div class="page-title">${title}</div>
    ${searchHtml}
    <div style="flex:1"></div>
    <div class="avatar-btn">
      <span class="avatar">${role}</span>
      <span style="font-size:13px;color:rgba(255,255,255,.85)">${user}</span>
      ${ic('chevronDown', '', 15)}
    </div>`;
}
