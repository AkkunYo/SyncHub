/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const styles = readFileSync(resolve(process.cwd(), 'src/styles.css'), 'utf8')

describe('console accessibility styles', () => {
  it('keeps a high-contrast keyboard focus indicator on form controls', () => {
    expect(styles).toContain('outline: 2px solid #2563eb;')
    expect(styles).toContain('outline-offset: 2px;')
  })

  it('uses readable contrast for compact sidebar labels', () => {
    expect(styles).not.toContain('color: #a1a1aa;')
  })
})
