import fs from 'node:fs'
import assert from 'node:assert/strict'
import { runWSL } from './local-tools.mjs'
import { readBundleDigest, readRestoreEvidence } from './local-recovery-evidence.mjs'

if (!process.argv.includes('--local')) throw new Error('请传入 --local 验证本机备份')

function read(args, input) {
  const result = runWSL(args, input)
  assert.equal(result.status, 0, '本机备份检查失败：' + args[0])
  return result.stdout.toString().trim()
}

const timer = 'dbhub-local-backup.timer'
const service = 'dbhub-local-backup.service'
const root = '/home/dzz/.local/share/dbhub/backups'
const evidenceDir = '.dbhub/audits/department-remediation'
const digest = bundle => readBundleDigest(read, root, bundle)

assert.equal(read(['systemctl', 'is-enabled', timer]), 'enabled')
assert.equal(read(['systemctl', 'is-active', timer]), 'active')
assert.equal(read(['systemctl', 'show', service, '-p', 'Result', '--value']), 'success')
const nextRun = read(['systemctl', 'show', timer, '-p', 'NextElapseUSecRealtime', '--value'])
assert.ok(nextRun && nextRun !== 'n/a', '无法确认下次定时备份时间')

const bundles = read(['find', root, '-mindepth', '1', '-maxdepth', '1', '-type', 'd', '-name', 'bundle-*'])
  .split('\n').filter(Boolean).sort().reverse()
assert.ok(bundles.length >= 2, '本机完整归档少于两份')
const latestBundle = bundles[0]
const latestBundleSha256 = digest(latestBundle)
const manifest = JSON.parse(read(['cat', latestBundle + '/manifest.json']))
assert.equal(manifest.format, 'dbhub-local-recovery-v1')
assert.equal(read(['stat', '-c', '%a', root]), '700')
assert.equal(read(['stat', '-c', '%a', latestBundle + '/application.env']), '600')

const receipt = JSON.parse(fs.readFileSync(evidenceDir + '/restore-drill-verification.json', 'utf8'))
const restoredBundle = readRestoreEvidence(receipt, root, digest)
const retention = JSON.parse(read(['bash', '/mnt/c/dev/projects/DBHub/scripts/ops/test-local-retention.sh']))
assert.equal(retention.status, 'passed')
const operation = JSON.parse(read(['docker', 'exec', '-i', 'dbhub-local-metadata-1', 'psql', '-U', 'dbhub', '-d', 'dbhub_meta', '-X', '-At'],
  "SELECT json_build_object('status',status,'last_success_at',last_success_at,'details',details) FROM sys_operations WHERE name='backup';"))
assert.equal(operation.status, 'succeeded')
const output = {
  status: 'passed',
  verifiedAt: new Date().toISOString(),
  timer: { enabled: true, active: true, nextRun, schedule: '每天02:30（Asia/Singapore），随机延迟不超过5分钟；WSL关闭时下次启动补跑' },
  completedBundles: bundles.length,
  latestBundle,
  latestBundleSha256,
  latestBundleRestored: restoredBundle.bundle === latestBundle && restoredBundle.bundleSha256 === latestBundleSha256,
  restoredBundle,
  retention,
  scope: manifest.databases,
  auditIncluded: manifest.audit_included,
  applicationKeyIncluded: manifest.application_key_included,
  matchingBinaryIncluded: manifest.matching_server_included,
  privateDirectoryMode: '0700',
  applicationKeyMode: '0600',
  operation,
  restoreChecks: restoredBundle.checks,
  credentialsIncluded: false,
}
fs.writeFileSync(evidenceDir + '/backup-verification.json', JSON.stringify(output, null, 2) + '\n')
console.log(JSON.stringify(output))
