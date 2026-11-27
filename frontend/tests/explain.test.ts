import assert from 'node:assert/strict'
import test from 'node:test'
import { isExplainStatement, postgresPlanNodes } from '../src/views/query/explain.ts'

test('执行解释只匹配提交的语句前缀', () => {
  for (const sql of ['EXPLAIN SELECT 1', '  explain\nSELECT 1', '-- note\n/* hint */ EXPLAIN SELECT 1']) {
    assert.equal(isExplainStatement(sql), true, sql)
  }
  for (const sql of [
    'SELECT 1 AS "QUERY PLAN"', "SELECT 'EXPLAIN SELECT 1'", 'EXPLAINED', '-- EXPLAIN',
    '/* never closed EXPLAIN SELECT 1', '/*! SELECT 1 */ EXPLAIN SELECT 2',
  ]) {
    assert.equal(isExplainStatement(sql), false, sql)
  }
})

test('执行解释按提交时的数据库方言识别前置注释', () => {
  for (const sql of [
    '--执行注释\nEXPLAIN SELECT 1',
    '--执行注释\r\nEXPLAIN SELECT 1',
    '/* 外层 /* 内层 */ 结束 */ EXPLAIN SELECT 1',
    '/*! 普通 PostgreSQL 注释 */ EXPLAIN SELECT 1',
  ]) {
    assert.equal(isExplainStatement(sql, 'postgres'), true, sql)
    assert.equal(isExplainStatement(sql, 'mysql'), false, sql)
  }
  assert.equal(isExplainStatement('/* 外层 /* 内层 */ EXPLAIN SELECT 1', 'postgres'), false)
  assert.equal(isExplainStatement('--注释\nSELECT 1 AS "QUERY PLAN"', 'postgres'), false)
  assert.equal(isExplainStatement('-- note\r\n/* hint */ EXPLAIN SELECT 1', 'mysql'), true)
  const mysqlHashComment = '# MySQL 计划\r\nEXPLAIN FORMAT=JSON SELECT 1'
  assert.equal(isExplainStatement(mysqlHashComment, 'mysql'), true)
  assert.equal(isExplainStatement(mysqlHashComment, 'postgres'), false)
  assert.equal(isExplainStatement('# EXPLAIN SELECT 1', 'mysql'), false)
  assert.equal(isExplainStatement('# 说明\nSELECT 1 AS `EXPLAIN`', 'mysql'), false)
})

test('PostgreSQL 计划保持层级与每轮值，区分估计、实测和缺项', () => {
  const lines = [
    ['Nested Loop  (cost=0.00..3.28 rows=1 width=4) (actual time=0.005..0.009 rows=1 loops=1)'],
    ['  ->  Seq Scan on first  (cost=0.00..1.00 rows=1 width=4) (actual time=0.000..0.000 rows=0 loops=2)'],
    ['        Filter: (id > 10)'],
    ['  ->  Index Scan using idx on second  (cost=0.28..2.28 rows=1 width=4) (never executed)'],
    ['  ->  Result  (cost=0.00..0.01 rows=1 width=4) (actual rows=1 loops=1)'],
    ['Planning Time: 0.075 ms'],
    ['Execution Time: 0.024 ms'],
  ]
  const result = postgresPlanNodes(['QUERY PLAN'], lines)
  assert.deepEqual(result.map(node => [node.depth, node.label]), [
    [0, 'Nested Loop'], [1, 'Seq Scan on first'], [1, 'Index Scan using idx on second'], [1, 'Result'],
  ])
  assert.equal(result[1]?.cost, '0.00..1.00')
  assert.equal(result[1]?.estimatedRows, '1')
  assert.equal(result[1]?.actualRows, '0')
  assert.equal(result[1]?.actualTime, '0.000..0.000')
  assert.equal(result[1]?.loops, '2')
  assert.equal(result[2]?.actualRows, undefined)
  assert.equal(result[3]?.actualRows, '1')
  assert.equal(result[3]?.actualTime, undefined)
  assert.deepEqual(postgresPlanNodes(['QUERY PLAN'], [[[{ Plan: {} }]]]), [])
  assert.deepEqual(postgresPlanNodes(['select_type', 'rows'], [['SIMPLE', 0]]), [])
})
