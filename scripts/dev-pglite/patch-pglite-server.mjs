/**
 * 对 pglite-server/dist/index.js 做一次性幂等补丁：
 * 启动握手只回传了 server_version 一个 ParameterStatus，导致 pgx simple
 * 协议（客户端转义参数所需）拒绝执行。这里补发 standard_conforming_strings、
 * client_encoding 等标准参数状态。补丁幂等，已打过则跳过。
 */
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const target = join(here, 'node_modules/pglite-server/dist/index.js');
let src = readFileSync(target, 'utf8');

if (src.includes('standard_conforming_strings')) {
  console.log('[patch] pglite-server 已包含标准参数状态，跳过');
} else {
  const oldBlock = `  const parameterStatus = new GrowableOffsetBuffer();
  const paramKey = "server_version";
  const paramValue = "pglite";
  parameterStatus.write("S");
  parameterStatus.writeUint32BE(6 + Buffer.byteLength(paramKey) + Buffer.byteLength(paramValue));
  parameterStatus.write(paramKey);
  parameterStatus.writeUint8(0);
  parameterStatus.write(paramValue);
  parameterStatus.writeUint8(0);`;

  const newBlock = `  const parameterStatus = new GrowableOffsetBuffer();
  const startupParams = [
    ["server_version", "18.3 (PGlite)"],
    ["server_encoding", "UTF8"],
    ["client_encoding", "UTF8"],
    ["DateStyle", "ISO, MDY"],
    ["TimeZone", "UTC"],
    ["integer_datetimes", "on"],
    ["standard_conforming_strings", "on"],
    ["is_superuser", "on"]
  ];
  for (const [paramKey, paramValue] of startupParams) {
    parameterStatus.write("S");
    parameterStatus.writeUint32BE(6 + Buffer.byteLength(paramKey) + Buffer.byteLength(paramValue));
    parameterStatus.write(paramKey);
    parameterStatus.writeUint8(0);
    parameterStatus.write(paramValue);
    parameterStatus.writeUint8(0);
  }`;

  if (!src.includes(oldBlock)) {
    throw new Error('未找到 pglite-server 握手参数构造代码，补丁失败（上游版本可能已变化）');
  }
  src = src.replace(oldBlock, newBlock);
  writeFileSync(target, src);
  console.log('[patch] pglite-server 握手 ParameterStatus 已补全');
}
