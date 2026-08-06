/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const targetsPage = readFileSync(resolve(process.cwd(), 'src/pages/TargetsPage.vue'), 'utf8')

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

describe('target list responsive layout', () => {
  it.each([320, 390, 620])('keeps target cards readable at %ipx', (viewportWidth) => {
    const breakpoint = 720
    const mobileRules = blockFor(targetsPage, `@media (max-width: ${breakpoint}px)`)
    const row = declarationsFor(mobileRules, '.connection-table tr')
    const targetCells = declarationsFor(mobileRules, '.connection-table td')
    const content = declarationsFor(mobileRules, '.primary-cell,')

    expect(viewportWidth).toBeLessThanOrEqual(breakpoint)
    expect(row).toContain('grid-template-columns: minmax(0, 1fr);')
    expect(targetCells).toContain('grid-column: 1;')
    expect(targetCells).toContain('grid-column: 1;')
    expect(content).toContain('min-width: 0;')
  })
})
