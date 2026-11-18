import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, realpath, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { readBundleDigest, readRestoreEvidence, requireBundlePath } from './local-recovery-evidence.mjs'

const name = 'bundle-20261113T030000Z-00000000-0000-0000-0000-000000000001'
const hash = data => createHash('sha256').update(data).digest('hex')
const root = '/backup'
const bundle = root + '/' + name
const receipt = {
  status: 'passed', bundle, bundleSha256: hash('sealed'),
  checks: [{ name: 'restored metadata', status: 'passed' }], finishedAt: '2026-11-13T03:00:00Z',
}

test('a rotated latest backup does not replace the archive named in the restore receipt', () => {
  const requested = []
  const output = readRestoreEvidence(receipt, root, candidate => {
    requested.push(candidate)
    return receipt.bundleSha256
  })
  assert.deepEqual(requested, [bundle])
  assert.deepEqual(output, { bundle, bundleSha256: receipt.bundleSha256, checks: 1, finishedAt: receipt.finishedAt })
})

test('a legacy, mismatched or failed receipt cannot prove the current archive was restored', () => {
  assert.throws(() => readRestoreEvidence({ ...receipt, bundleSha256: undefined }, root, () => {
    assert.fail('legacy receipt should be rejected before reading the archive')
  }), /缺少归档指纹/)
  assert.throws(() => readRestoreEvidence(receipt, root, () => hash('replaced')), /指纹.*不一致/)
  assert.throws(() => readRestoreEvidence({ ...receipt, status: 'failed' }, root, () => receipt.bundleSha256), /尚未通过/)
  assert.throws(() => readRestoreEvidence({ ...receipt, checks: [] }, root, () => receipt.bundleSha256), /检查明细/)
})

test('only direct canonical archive names under the expected backup directory are accepted', () => {
  for (const candidate of ['/backup-other/' + name, root + '/nested/' + name, root + '/../' + name, root + '/latest']) {
    assert.throws(() => requireBundlePath(root, candidate), /直接子目录/)
  }
})

test('the shell check verifies the complete index and rejects modified, omitted and linked files', async () => {
  const scratch = await mkdtemp(join(await realpath(tmpdir()), 'dbhub-recovery-proof-'))
  try {
    const archive = join(scratch, name)
    await mkdir(archive)
    const files = ['manifest.json', 'application.env', 'local-compose.yaml', 'metadata-roles.sql', 'business-roles.sql',
      'audit.tar.gz', 'server.tar.gz', 'healthcheck.tar.gz', 'dbhub_meta.dump', 'dbhub_demo.dump', 'tms.dump',
      'wms.dump', 'dbhub_analytics.dump', 'demo-identities.json']
    const indexPath = join(archive, 'SHA256SUMS')
    const index = names => names.map(file => `${hash(file)}  ./${file}\n`).join('')
    for (const file of files) await writeFile(join(archive, file), file)
    await writeFile(indexPath, index(files))

    function read() {
      return readBundleDigest(args => {
        const result = spawnSync(args[0], args.slice(1), { encoding: 'utf8' })
        assert.equal(result.status, 0, '归档校验失败')
        return result.stdout
      }, scratch, archive)
    }
    assert.equal(read(), hash(index(files)))
    await writeFile(join(archive, 'dbhub_meta.dump'), 'different')
    assert.throws(read, /归档校验失败/)
    await writeFile(join(archive, 'dbhub_meta.dump'), 'dbhub_meta.dump')
    for (const omitted of ['application.env', 'demo-identities.json']) {
      await writeFile(indexPath, index(files.filter(file => file !== omitted)))
      assert.throws(read, /归档校验失败/)
    }
    await writeFile(indexPath, index(files) + `${hash('outside')}  ../outside\n`)
    assert.throws(read, /归档校验失败/)
    await writeFile(indexPath, index(files))
    await writeFile(join(scratch, 'outside'), 'dbhub_meta.dump')
    await rm(join(archive, 'dbhub_meta.dump'))
    await symlink(join(scratch, 'outside'), join(archive, 'dbhub_meta.dump'))
    assert.throws(read, /归档校验失败/)
  } finally {
    await rm(scratch, { recursive: true, force: true })
  }
})
