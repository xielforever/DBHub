/**
 * PGlite 线协议服务（仅开发/沙箱环境）：
 * 把进程内 PGlite 通过 PostgreSQL 线协议暴露在 127.0.0.1:5432，
 * 供本机 Go 后端像连普通 Postgres 一样连接，沙箱内替代 Docker Postgres。
 * 生产交付仍走 docker-compose 真实 PostgreSQL，禁止使用本脚本。
 *
 * 首次启动时自动：
 *  1. 读取 backend/internal/db/migrations/*.sql 建表（幂等，按 schema_migrations 记录）；
 *  2. 写入三个演示账号 admin/dev1/ro1（与后端 PBKDF2-SHA256 编码一致）；
 *  3. 灌入演示业务库 schema「shop」（见 seed-demo-shop.mjs，已存在则跳过）。
 *
 * 环境变量：
 *  PGLITE_PORT（默认 5432）、PGLITE_HOST（默认 127.0.0.1）、
 *  PGLITE_DATA_DIR（默认脚本同级 data/）。
 */
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import crypto from 'node:crypto';
// 注意：pglite-server 必须在补丁写盘之后再动态加载——ESM 静态 import 的
// 源码读取发生在任何模块体执行之前，静态引入会让补丁来不及生效。
import './patch-pglite-server.mjs';
import { PGlite } from '@electric-sql/pglite';
import { seedShop } from './seed-demo-shop.mjs';
const { createServer, LogLevel } = await import('pglite-server');

const here = dirname(fileURLToPath(import.meta.url));
const DATA_DIR = process.env.PGLITE_DATA_DIR || join(here, 'data');
const MIGRATIONS_DIR = join(here, '..', '..', 'backend', 'internal', 'db', 'migrations');
const PORT = Number(process.env.PGLITE_PORT) || 5432;
const HOST = process.env.PGLITE_HOST || '127.0.0.1';

const db = new PGlite(DATA_DIR);
await db.waitReady;

// ---- 迁移（与后端 internal/db.Migrate 相同的版本记录约定）----
await db.exec(`
  CREATE TABLE IF NOT EXISTS schema_migrations (
    version    VARCHAR(255) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
`);
const files = readdirSync(MIGRATIONS_DIR).filter((f) => f.endsWith('.sql')).sort();
for (const name of files) {
  const res = await db.query('SELECT 1 FROM schema_migrations WHERE version = $1', [name]);
  if (res.rows.length > 0) continue;
  let sql = readFileSync(join(MIGRATIONS_DIR, name), 'utf8');
  // PGlite 未打包 uuid-ossp/pgcrypto；本项目不使用数据库端 UUID/加密函数，
  // 沙箱内剥离扩展安装语句（真实 Postgres 仍按原脚本由 Go 自动迁移安装）。
  if (name === '0001_extensions.sql') {
    sql = sql.replace(/^CREATE EXTENSION.*(?:uuid-ossp|pgcrypto).*$/gim, '-- (pglite) extension skipped');
  }
  // PGlite exec 一次执行整个多语句脚本（含 DO/触发器）。
  await db.exec('BEGIN');
  try {
    await db.exec(sql);
    await db.query('INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING', [name]);
    await db.exec('COMMIT');
    console.log('[pglite] migrated', name);
  } catch (err) {
    await db.exec('ROLLBACK');
    throw err;
  }
}

// ---- 演示账号（PBKDF2-HMAC-SHA256，210000 次迭代，编码与后端 internal/auth 完全一致）----
function hashPassword(password) {
  const salt = crypto.randomBytes(16);
  const key = crypto.pbkdf2Sync(password, salt, 210_000, 32, 'sha256');
  const b64 = (buf) => buf.toString('base64').replace(/=+$/, '');
  return `pbkdf2_sha256$210000$${b64(salt)}$${b64(key)}`;
}
const seedUsers = [
  ['admin', 'admin123', 'admin@dbhub.local', 'admin'],
  ['dev1', 'dev12345!', 'dev1@dbhub.local', 'developer'],
  ['ro1', 'ro123456', 'ro1@dbhub.local', 'readonly'],
];
for (const [username, password, email, roleCode] of seedUsers) {
  await db.query(
    `INSERT INTO sys_users (username, password_hash, email, role_id)
     SELECT $1, $2, $3, r.id FROM sys_roles r WHERE r.code = $4
     ON CONFLICT (username) DO NOTHING`,
    [username, hashPassword(password), email, roleCode],
  );
}
console.log('[pglite] seed users ensured (admin/dev1/ro1)');

// ---- 演示业务库 schema「shop」（幂等，已灌入则跳过）----
await seedShop(db);

// ---- 启动线协议服务（单工作连接，后端需 META_DB_MAX_CONNS=1）----
const server = createServer(db, { logLevel: LogLevel.Error });
server.listen(PORT, HOST, () => {
  console.log(`[pglite] postgres wire server listening on ${HOST}:${PORT}`);
});

const shutdown = () => {
  server.close(() => {
    console.log('[pglite] server closed');
    process.exit(0);
  });
};
process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);
