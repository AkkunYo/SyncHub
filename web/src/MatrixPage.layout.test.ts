/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const matrixPage = readFileSync(resolve(process.cwd(), 'src/pages/MatrixPage.vue'), 'utf8')
const styles = readFileSync(resolve(process.cwd(), 'src/styles.css'), 'utf8')

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
  it('uses a matrix-specific hook without changing other page action toolbars', () => {
    expect(matrixPage).toContain('class="page-actions matrix-page-actions"')
  })

  it.each([320, 390, 620])('contains the upstream selector and refresh button at %ipx', (viewportWidth) => {
    const breakpoint = 620
    const mobileRules = blockFor(styles, `@media (max-width: ${breakpoint}px)`)
    const actions = declarationsFor(mobileRules, '.matrix-page-actions')
    const field = declarationsFor(mobileRules, '.matrix-page-actions .compact-field')
    const select = declarationsFor(mobileRules, '.matrix-page-actions select')

    expect(viewportWidth).toBeLessThanOrEqual(breakpoint)
    expect(actions).toContain('display: grid;')
    expect(actions).toContain('grid-template-columns: minmax(0, 1fr) auto;')
    expect(actions).toContain('align-items: end;')
    expect(actions).toContain('width: 100%;')
    expect(field).toContain('min-width: 0;')
    expect(select).toContain('min-width: 0;')
    expect(select).toContain('width: 100%;')
  })
})
