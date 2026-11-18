export interface ReportParameter {
  name: string
  label: string
  type: 'text' | 'integer' | 'decimal' | 'date' | 'datetime' | 'boolean' | 'enum'
  required?: boolean
  default?: string | number | boolean | null
  default_relative_days?: number
  timezone?: string
  options?: { label: string; value: string }[]
  min?: number
  max?: number
  max_length?: number
}
export type ParameterValues = Record<string, string | number | boolean | null>

const integerText = /^[+-]?[0-9]+$/
const decimalText = /^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$/
const dateText = /^\d{4}-\d{2}-\d{2}$/
const dateTimeText = /^(\d{4}-\d{2}-\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,]\d+)?(?:Z|[+-](\d{2}):(\d{2}))$/
const textWhitespace = /^\p{White_Space}*$/u
const maxTextQueryLength = 2 * 4096 + 1
const int64Min = -9223372036854775808n
const int64Max = 9223372036854775807n

function scaledDecimal(text: string) {
  const [coefficient = '', exponent = '0'] = text.split('e')
  const scale = (coefficient.split('.')[1]?.length ?? 0) - Number(exponent)
  return { digits: BigInt(coefficient.replace('.', '')), scale }
}

// Align decimal places without rounding the entered value or a large identifier.
function compareToBound(text: string, bound: number) {
  const left = scaledDecimal(text)
  const right = scaledDecimal(String(bound))
  const scale = Math.max(left.scale, right.scale)
  return left.digits * 10n ** BigInt(scale - left.scale) - right.digits * 10n ** BigInt(scale - right.scale)
}

function isCalendarDate(value: string) {
  if (!dateText.test(value)) return false
  const date = new Date(`${value}T00:00:00Z`)
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value
}

function isZonedDateTime(value: string) {
  const time = dateTimeText.exec(value)
  if (!time) return false
  const [, date = '', hour, minute, second, zoneHour = '0', zoneMinute = '0'] = time
  // Match the existing Go RFC3339Nano parser, including its legacy offset bounds.
  return isCalendarDate(date) && Number(hour) < 24 && Number(minute) < 60 && Number(second) < 60 &&
    Number(zoneHour) <= 24 && Number(zoneMinute) <= 60
}

export function reportParameterError(parameter: ReportParameter, value: ParameterValues[string] | undefined): string | undefined {
  if (parameter.required) {
    let blank = value == null || value === ''
    if (typeof value === 'string' && parameter.type !== 'enum') {
      blank = parameter.type === 'text' ? textWhitespace.test(value) : !value.trim()
    }
    if (blank) return `请填写${parameter.label}`
  }
  if (value == null || value === '') return
  const invalid = `${parameter.label}的值、格式或范围无效`
  if (parameter.type === 'text' || parameter.type === 'enum') {
    if (typeof value !== 'string' || value.includes('\0') || [...value].length > (parameter.max_length || 1024)) return invalid
    if (parameter.type === 'enum' && !parameter.options?.some(option => option.value === value)) return invalid
    return
  }
  if (parameter.type === 'boolean') {
    return typeof value === 'boolean' || value === 'true' || value === 'false' ? undefined : invalid
  }
  if (parameter.type === 'date' || parameter.type === 'datetime') {
    if (typeof value !== 'string' || value.length > (parameter.max_length || 1024)) return invalid
    const valid = parameter.type === 'date' ? isCalendarDate(value) : isZonedDateTime(value)
    return valid ? undefined : invalid
  }
  if (typeof value !== 'string' || (parameter.type !== 'integer' && parameter.type !== 'decimal')) return
  const syntax = parameter.type === 'integer' ? integerText : decimalText
  if (value.length > 128 || value.trim() !== value || !syntax.test(value)) return invalid
  if (parameter.type === 'integer') {
    const integer = BigInt(value)
    if (integer < int64Min || integer > int64Max) return invalid
  }
  if (typeof parameter.min === 'number' && Number.isFinite(parameter.min) && compareToBound(value, parameter.min) < 0n) return invalid
  if (typeof parameter.max === 'number' && Number.isFinite(parameter.max) && compareToBound(value, parameter.max) > 0n) return invalid
}

// Only declared scalar values enter a report URL. Database authorization is
// always enforced by the server and cannot be represented by a viewer filter.
export function parametersFromQuery(definitions: ReportParameter[], query: Record<string, unknown>): ParameterValues {
  const values: ParameterValues = Object.create(null)
  for (const p of definitions) {
    const raw = query[`p_${p.name}`]
    // A supplementary character occupies two UTF-16 code units. The leading
    // escape can add one more unit beyond the longest approved text.
    const queryLimit = p.type === 'text' ? maxTextQueryLength : 4096
    if (typeof raw === 'string' && raw.length <= queryLimit) {
      if (raw === '~null') values[p.name] = null
      else if (raw.startsWith('~~')) values[p.name] = raw.slice(1)
      else values[p.name] = p.type === 'boolean' && ['true', 'false'].includes(raw) ? raw === 'true' : raw
    }
  }
  return values
}

export function parameterQuery(values: ParameterValues): Record<string, string> {
  const query: Record<string, string> = Object.create(null)
  for (const [key, value] of Object.entries(values)) query[`p_${key}`] = value === null ? '~null' : typeof value === 'string' && value.startsWith('~') ? '~' + value : String(value)
  return query
}

export function parameterDefaults(definitions: ReportParameter[], now = new Date()): ParameterValues {
  const values: ParameterValues = Object.create(null)
  for (const p of definitions) {
    if (p.default !== undefined) values[p.name] = p.default
    else if (p.default_relative_days !== undefined) {
      // Use calendar days in the declared business timezone, not browser offset
      // arithmetic that changes around midnight or daylight-saving transitions.
      const parts = new Intl.DateTimeFormat('en-CA', { timeZone: p.timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(now)
      const part = (type: string) => parts.find(p => p.type === type)?.value || ''
      const day = new Date(`${part('year')}-${part('month')}-${part('day')}T12:00:00Z`)
      day.setUTCDate(day.getUTCDate() + p.default_relative_days)
      values[p.name] = day.toISOString().slice(0, 10)
    }
  }
  return values
}
