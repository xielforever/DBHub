import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'

// Exercise directive lifecycle with DOM state doubles, without business APIs or a browser.
test('加载区域保护新旧子节点，保留表头空间，退出与卸载恢复状态', async () => {
  class Element {
    constructor() {
      this.children = []; this.parent = null; this.dataset = {}; this.inert = false
      this.attributes = new Map(); this.styles = new Map(); this.classes = new Set()
      this.classList = { add: x => this.classes.add(x), remove: x => this.classes.delete(x) }
      this.style = { setProperty: (k, v) => this.styles.set(k, v), removeProperty: k => this.styles.delete(k) }
    }
    getAttribute(k) { return this.attributes.get(k) ?? null }
    setAttribute(k, v) { this.attributes.set(k, v) }
    removeAttribute(k) { this.attributes.delete(k) }
    append(child) { child.parent = this; this.children.push(child) }
    after(child) { child.parent = this.parent; this.parent.children.push(child) }
    remove() { if (this.parent) this.parent.children = this.parent.children.filter(x => x !== this) }
    contains(child) { return this.children.includes(child) }
    focus() { document.activeElement = this }
    getBoundingClientRect() { return { height: 240, top: 10 } }
    querySelector() { return { getBoundingClientRect: () => ({ bottom: 50 }) } }
  }
  const document = { createElement: () => new Element(), activeElement: null }
  const lifecycles = []
  const context = vm.createContext({ HTMLElement: Element, document, Map, WeakMap, Array, Math })
  const source = await readFile(new URL('../src/utils/data-loading.ts', import.meta.url), 'utf8')
  const module = new vm.SourceTextModule(ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText, { context })
  await module.link(async name => {
    const values = name === 'vue' ? {
      createVNode: (_component, props) => props, render: (node, host) => { host.rendered = node },
    } : name.endsWith('.vue') ? { default: {} } : {
      loadingTimings: () => [150, 5000],
      createLoadingFeedback: () => ({ set: busy => lifecycles.push(busy), dispose: () => lifecycles.push('disposed') }),
    }
    return new vm.SyntheticModule(Object.keys(values), function () {
      for (const [key, value] of Object.entries(values)) this.setExport(key, value)
    }, { context })
  })
  await module.evaluate()
  const directive = module.namespace.dataLoading
  const parent = new Element(), region = new Element(), row = new Element(), disabled = new Element()
  disabled.inert = true
  parent.append(region); region.append(row); region.append(disabled)
  region.setAttribute('aria-busy', 'true') // Vue may have updated a bound attribute already.
  document.activeElement = row
  directive.mounted(region, { value: true })
  assert.equal(row.inert, true)
  assert.equal(document.activeElement, region)
  assert.equal(region.styles.get('--db-loading-header-height'), '40px')
  const added = new Element(); region.append(added)
  directive.updated(region, { value: true })
  assert.equal(added.inert, true)
  assert.equal(region.children.find(x => x.className === 'db-loading-region-status').inert, false)
  directive.updated(region, { value: false })
  assert.equal(row.inert, false)
  assert.equal(added.inert, false)
  assert.equal(disabled.inert, true)
  assert.equal(region.getAttribute('aria-busy'), 'false')
  assert.equal(region.styles.size, 0)
  directive.updated(region, { value: true })
  directive.beforeUnmount(region)
  assert.equal(parent.children.length, 1)
  assert.equal(row.inert, false)
  assert.deepEqual(lifecycles, [true, false, true, false, 'disposed'])
})
