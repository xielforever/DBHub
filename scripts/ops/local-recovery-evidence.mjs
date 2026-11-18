import assert from 'node:assert/strict'

const bundleName = /^bundle-\d{8}T\d{6}Z-[0-9a-f-]{36}$/
const sha256 = /^[0-9a-f]{64}$/

export function requireBundlePath(root, bundle) {
  assert.equal(typeof bundle, 'string', '恢复回执缺少归档路径')
  const prefix = root + '/'
  assert.ok(bundle.startsWith(prefix) && bundleName.test(bundle.slice(prefix.length)),
    '归档必须是指定备份目录中的直接子目录')
  return bundle
}

export function readBundleDigest(run, root, bundle) {
  requireBundlePath(root, bundle)
  const result = run(['bash', '-c', String.raw`
    set -euo pipefail
    bundle=$1
    [[ -d $bundle && ! -L $bundle && $(realpath -e -- "$bundle") == "$bundle" ]]
    cd -- "$bundle"
    [[ -f SHA256SUMS && ! -L SHA256SUMS ]]
    before=$(sha256sum -- SHA256SUMS)
    awk '
      length($1) != 64 || $1 !~ /^[0-9a-f]+$/ || NF != 2 { exit 1 }
      {
        file = $2
        sub(/^\*/, "", file)
        sub(/^\.\//, "", file)
        if (file !~ /^[A-Za-z0-9][A-Za-z0-9_.-]*$/ || seen[file]++) exit 1
        print file
      }
    ' SHA256SUMS | {
      declare -A indexed=()
      while IFS= read -r file; do
        [[ -f $file && ! -L $file ]]
        indexed[$file]=1
      done
      for file in manifest.json application.env local-compose.yaml metadata-roles.sql business-roles.sql audit.tar.gz \
        server.tar.gz healthcheck.tar.gz dbhub_meta.dump dbhub_demo.dump tms.dump wms.dump dbhub_analytics.dump; do
        [[ -v "indexed[$file]" ]]
      done
      if [[ -e demo-identities.json || -L demo-identities.json ]]; then
        [[ -v 'indexed[demo-identities.json]' ]]
      fi
    }
    sha256sum --strict -c SHA256SUMS >/dev/null
    after=$(sha256sum -- SHA256SUMS)
    [[ $before == "$after" ]]
    printf '%s\n' "$after" | cut -d ' ' -f 1
  `, 'bash', bundle])
  const digest = result.toString().trim()
  assert.ok(sha256.test(digest), '归档校验未返回有效指纹')
  return digest
}

export function requireMatchingDigest(expected, actual) {
  requireReceiptDigest(expected)
  assert.ok(expected === actual, '归档指纹与演练回执不一致')
}

function requireReceiptDigest(digest) {
  assert.ok(typeof digest === 'string' && sha256.test(digest), '旧回执缺少归档指纹，需重新演练')
}

export function readRestoreEvidence(receipt, root, readDigest) {
  assert.equal(receipt.status, 'passed', '恢复演练尚未通过')
  assert.ok(Array.isArray(receipt.checks) && receipt.checks.length > 0 &&
    receipt.checks.every(check => check.status === 'passed'), '恢复回执缺少通过的检查明细')
  const bundle = requireBundlePath(root, receipt.bundle)
  requireReceiptDigest(receipt.bundleSha256)
  requireMatchingDigest(receipt.bundleSha256, readDigest(bundle))
  return {
    bundle,
    bundleSha256: receipt.bundleSha256,
    checks: receipt.checks.length,
    finishedAt: receipt.finishedAt,
  }
}
