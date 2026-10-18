import type { ChartConfig } from '../../components/business/chart/ChartCard.vue'

export type ReportDisplay = NonNullable<ChartConfig['display']>

export function displayLabel(column: string, display?: ReportDisplay): string {
  const labels = display?.labels
  return labels &&
    Object.prototype.hasOwnProperty.call(labels, column) &&
    typeof labels[column] === 'string'
    ? labels[column]!.trim() || column
    : column
}

export function displayUnit(unit?: string): string {
  const labels: Record<string, string> = {
    CNY_10000: '万元',
    CNY: '元',
    item_count: '项',
    item_quantity: '件',
    percent: '%',
  }
  return unit && Object.prototype.hasOwnProperty.call(labels, unit)
    ? labels[unit]!
    : unit || ''
}

export function reportNumberRounded(
  value: unknown,
  display?: ReportDisplay,
): boolean {
  if (
    display?.fraction_digits === undefined ||
    value === null ||
    value === undefined
  )
    return false
  const normalize = (text: string) =>
    text.includes('.') ? text.replace(/0+$/, '').replace(/\.$/, '') : text
  return (
    normalize(formatReportNumber(value, { grouping: false })) !==
    normalize(formatReportNumber(value, { ...display, grouping: false }))
  )
}

// Format decimal text directly. Converting a database decimal to Number would
// silently change large integers and financial fractions before formatting.
export function formatReportNumber(
  value: unknown,
  display?: ReportDisplay,
): string {
  if (value === null || value === undefined) return '—'
  if (typeof value !== 'string' && typeof value !== 'number')
    return String(value)
  const original = String(value).trim()
  if (original.length > 4096) return original
  const match = /^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$/.exec(original)
  if (!match || !(match[2] || match[3])) return original
  const exponent = Number(match[4] || 0)
  if (!Number.isInteger(exponent) || Math.abs(exponent) > 1000) return original
  let digits = (match[2] || '') + (match[3] || '')
  let point = (match[2] || '').length + exponent
  if (point <= 0) {
    digits = '0'.repeat(1 - point) + digits
    point = 1
  }
  if (point >= digits.length) digits += '0'.repeat(point - digits.length)
  let whole = digits.slice(0, point).replace(/^0+(?=\d)/, '') || '0'
  let fraction = digits.slice(point)
  const places = display?.fraction_digits
  if (
    places !== undefined &&
    Number.isInteger(places) &&
    places >= 0 &&
    places <= 12
  ) {
    const padded = fraction.padEnd(places, '0')
    let kept = whole + padded.slice(0, places)
    if (fraction.length > places && fraction[places]! >= '5')
      kept = (BigInt(kept) + 1n).toString()
    kept = kept.padStart(places + 1, '0')
    whole = places ? kept.slice(0, -places) : kept
    fraction = places ? kept.slice(-places) : ''
  }
  const negative = match[1] === '-' && /[1-9]/.test(whole + fraction) ? '-' : ''
  if (display?.grouping !== false) {
    const groups: string[] = []
    for (let end = whole.length; end > 0; end -= 3)
      groups.push(whole.slice(Math.max(0, end - 3), end))
    whole = groups.reverse().join(',')
  }
  return negative + whole + (fraction ? '.' + fraction : '')
}
