/**
 * 演示业务库：在 PGlite 中创建独立 schema「shop」并灌入订单业务表与数据，
 * 供「订单核心库」数据源走通 数据库→模式→表→预览→SQL 执行 的完整闭环。
 * 平台自身的 sys_* 表位于 public，与业务 schema 隔离。
 *
 * - 作为模块被 server.mjs 引用时调用 seedShop(db)：已存在则跳过，幂等快速；
 * - 独立执行时重建（DROP SCHEMA CASCADE），要求线服务已停止，禁止两个
 *   PGlite 实例同时打开同一数据目录。
 */
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { PGlite } from '@electric-sql/pglite';

const here = dirname(fileURLToPath(import.meta.url));
// 数据目录可用 PGLITE_DATA_DIR 覆盖，默认脚本同级 data/
const DATA_DIR = process.env.PGLITE_DATA_DIR || join(here, 'data');

export async function seedShop(db, { recreate = false } = {}) {
  if (!recreate) {
    const exists = await db.query(
      `SELECT 1 FROM information_schema.tables
       WHERE table_schema = 'shop' AND table_name = 'orders' LIMIT 1`,
    );
    if (exists.rows.length > 0) {
      const stats = {};
      for (const t of ['customers', 'products', 'orders', 'order_items']) {
        stats[t] = (await db.query(`SELECT COUNT(*) c FROM shop.${t}`)).rows[0].c;
      }
      console.log('[pglite] shop schema 已存在，跳过灌入', stats);
      return stats;
    }
  }

  await db.exec(`
  DROP SCHEMA IF EXISTS shop CASCADE;
  CREATE SCHEMA shop;

  CREATE TABLE shop.customers (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(64) NOT NULL,
    email       VARCHAR(128),
    city        VARCHAR(32),
    level       VARCHAR(16) NOT NULL DEFAULT '普通',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE shop.products (
    id          SERIAL PRIMARY KEY,
    sku_code    VARCHAR(40) NOT NULL UNIQUE,
    title       VARCHAR(128) NOT NULL,
    category    VARCHAR(32) NOT NULL,
    price       NUMERIC(10,2) NOT NULL,
    stock       INTEGER NOT NULL DEFAULT 0,
    is_online   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE shop.orders (
    id            SERIAL PRIMARY KEY,
    order_no      VARCHAR(32) NOT NULL UNIQUE,
    customer_id   INTEGER REFERENCES shop.customers(id),
    status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    total_amount  NUMERIC(12,2) NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );

  CREATE TABLE shop.order_items (
    id          SERIAL PRIMARY KEY,
    order_id    INTEGER REFERENCES shop.orders(id) ON DELETE CASCADE,
    product_id  INTEGER REFERENCES shop.products(id),
    qty         INTEGER NOT NULL DEFAULT 1,
    price       NUMERIC(10,2) NOT NULL
  );

  CREATE INDEX idx_orders_created ON shop.orders(created_at DESC);
  CREATE INDEX idx_order_items_order ON shop.order_items(order_id);
`);

  // ---- 客户 24 ----
  const cities = ['上海', '北京', '深圳', '杭州', '广州', '成都', '武汉', '南京'];
  const surnames = ['王', '李', '张', '刘', '陈', '杨', '赵', '黄', '周', '吴'];
  const given = ['伟', '芳', '娜', '敏', '静', '磊', '军', '洋', '勇', '艳', '杰', '娟'];
  const levels = ['普通', '普通', '普通', '银卡', '银卡', '金卡'];
  for (let i = 1; i <= 24; i++) {
    const name = surnames[i % surnames.length] + given[(i * 7) % given.length];
    await db.query(
      `INSERT INTO shop.customers (name, email, city, level, created_at)
       VALUES ($1,$2,$3,$4, NOW() - ($5 || ' days')::interval)`,
      [name, `user${i}@example.com`, cities[i % cities.length], levels[i % levels.length], (i * 13) % 200 + 1],
    );
  }

  // ---- 商品 28 ----
  const cats = ['数码', '家电', '服饰', '食品', '图书', '美妆'];
  const productNames = [
    '无线蓝牙耳机', '机械键盘', '4K 显示器', '人体工学椅', '固态硬盘 1TB', '智能手表',
    '便携投影仪', '降噪麦克风', '电动牙刷', '空气炸锅', '扫地机器人', '加湿器',
    '纯棉 T 恤', '冲锋衣', '跑步鞋', '双肩背包', '牛仔裤', '太阳镜',
    '坚果礼盒', '冷萃咖啡液', '燕麦饼干', '橄榄油', '黑巧克力', '气泡水',
    'SQL 进阶实战', '数据库系统概念', '高性能 MySQL', '算法导论',
  ];
  for (let i = 0; i < productNames.length; i++) {
    const price = (19 + ((i * 37) % 480) + (i % 10)).toFixed(2);
    const stock = (i * 53) % 900;
    const sku = `SKU-${String(1001 + i)}`;
    await db.query(
      `INSERT INTO shop.products (sku_code, title, category, price, stock, is_online, created_at)
       VALUES ($1,$2,$3,$4,$5,$6, NOW() - ($7 || ' days')::interval)`,
      [sku, productNames[i], cats[i % cats.length], price, stock, i % 13 !== 0, (i * 11) % 300 + 1],
    );
  }

  // ---- 订单（近 14 天）+ 订单明细 ----
  const statuses = ['paid', 'paid', 'shipped', 'shipped', 'done', 'done', 'done', 'pending', 'cancelled'];
  let orderId = 1;
  for (let d = 13; d >= 0; d--) {
    const n = 12 + ((d * 5) % 12);
    for (let k = 0; k < n; k++) {
      const customer = 1 + ((orderId * 3) % 24);
      const status = statuses[(orderId + d) % statuses.length];
      const day = new Date(Date.UTC(2026, 8, 15) - d * 86400000 + (k % 9) * 3600000);
      const itemCount = 1 + (orderId % 3);
      let total = 0;
      const items = [];
      for (let j = 0; j < itemCount; j++) {
        const pid = 1 + ((orderId * 7 + j * 5) % 28);
        const qty = 1 + (orderId % 3);
        const price = Number(((pid * 13) % 460 + 19).toFixed(2));
        total += price * qty;
        items.push({ pid, qty, price });
      }
      const orderNo = `NO${day.toISOString().slice(0, 10).replace(/-/g, '')}${String(10000 + orderId).slice(1)}`;
      await db.query(
        `INSERT INTO shop.orders (order_no, customer_id, status, total_amount, created_at)
         VALUES ($1,$2,$3,$4,$5)`,
        [orderNo, customer, status, total.toFixed(2), day],
      );
      for (const it of items) {
        await db.query(
          `INSERT INTO shop.order_items (order_id, product_id, qty, price) VALUES ($1,$2,$3,$4)`,
          [orderId, it.pid, it.qty, it.price],
        );
      }
      orderId++;
    }
  }

  const stats = {};
  for (const t of ['customers', 'products', 'orders', 'order_items']) {
    stats[t] = (await db.query(`SELECT COUNT(*) c FROM shop.${t}`)).rows[0].c;
  }
  console.log('shop schema seeded:', stats);
  return stats;
}

// 独立执行：直接打开持久化数据目录并重建演示库（线服务必须已停止）。
if (import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  const db = new PGlite(DATA_DIR);
  await db.waitReady;
  try {
    await seedShop(db, { recreate: true });
  } finally {
    await db.close();
  }
}
