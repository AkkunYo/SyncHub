/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const matrixPage = readFileSync(resolve(process.cwd(), 'src/pages/MatrixPage.vue'), 'utf8')

function blockFor(source: string, selector: string): string {
  const selectorStart = source.indexOf(selector)
  if (selectorStart === -1) return ''

  const blockStart = source.indexOf('{', selectorStart + selector.length)
  if (blockStart === -1) return ''

  let depth = 1
  for (let index = blockStart + 1; index < source.length; index += 1) {
    if (source[index] === '{') depth += 1
    if (source[index] === '}') depth -= 1
    if (depth === 0) return source.slice(blockStart + 1, index)
  }
  return ''
}

function declarationsFor(source: string, selector: string): string {
  return blockFor(source, selector).replace(/\s+/g, ' ').trim()
}

describe('matrix action layout', () => {
  it('keeps the desktop workbench controls compact and ordered by task', () => {
    const actions = declarationsFor(matrixPage, '.matrix-page-actions')
    const controls = declarationsFor(
      matrixPage,
      '.matrix-page-actions input,\n.matrix-page-actions select,\n.matrix-page-actions button',
    )

    expect(matrixPage).toMatch(
      /class="page-actions matrix-page-actions"[\s\S]*class="search-field matrix-search-field"[\s\S]*class="compact-field matrix-upstream-field"[\s\S]*class="filter-field matrix-status-field"[\s\S]*class="secondary-button matrix-refresh-button"/,
    )
    expect(actions).toContain('display: grid;')
    expect(actions).toContain('min-width: 0;')
    expect(actions).toContain(
      'grid-template-columns: minmax(220px, 1fr) minmax(0, 220px) minmax(0, 160px) auto;',
    )
    expect(controls).toContain('min-width: 0;')
    expect(controls).toContain('min-height: 44px;')
  })

  it.each([320, 390])('gives search its own row and keeps filters usable at %ipx', (viewportWidth) => {
    const breakpoint = 620
    const mobileRules = blockFor(matrixPage, `@media (max-width: ${breakpoint}px)`)
    const actions = declarationsFor(mobileRules, '.matrix-page-actions')
    const search = declarationsFor(mobileRules, '.matrix-search-field')
    const upstream = declarationsFor(mobileRules, '.matrix-upstream-field')
    const status = declarationsFor(mobileRules, '.matrix-status-field')
    const refresh = declarationsFor(mobileRules, '.matrix-refresh-button')

    expect(viewportWidth).toBeLessThanOrEqual(breakpoint)
    expect(actions).toContain('grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr) 44px;')
    expect(search).toContain('grid-column: 1 / -1;')
    expect(upstream).toContain('min-width: 0;')
    expect(status).toContain('min-width: 0;')
    expect(refresh).toContain('width: 44px;')
    expect(refresh).toContain('min-height: 44px;')
  })

  it('hides zero metrics and exposes one refresh action in the empty state', () => {
    expect(matrixPage).toContain(
      'v-if="activeMatrix && rows.length > 0" class="metric-strip" aria-label="矩阵摘要"',
    )
    expect(matrixPage).toContain('v-if="rows.length > 0"\n            class="secondary-button matrix-refresh-button"')

    const emptyStateStart = matrixPage.indexOf(
      'v-else-if="rows.length === 0" class="state-panel matrix-empty-state"',
    )
    const emptyStateEnd = matrixPage.indexOf('</div>', emptyStateStart)
    const emptyState = matrixPage.slice(emptyStateStart, emptyStateEnd)

    expect(emptyStateStart).toBeGreaterThan(-1)
    expect(emptyState).toContain('class="matrix-empty-icon"')
    expect(emptyState).toContain('<h2>暂无上游资产</h2>')
    expect(emptyState).toContain('当前上游还没有可同步的资产，刷新后会在这里显示。')
    expect(emptyState.match(/<button/g)).toHaveLength(1)
    expect(emptyState).toContain('class="secondary-button matrix-empty-action"')
    expect(emptyState).toContain('@click="store.refreshAssets"')
  })
})
