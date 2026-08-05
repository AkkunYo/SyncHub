/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const styles = readFileSync(resolve(process.cwd(), 'src/styles.css'), 'utf8')

function ruleBody(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return styles.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`, 's'))?.[1] ?? ''
}

describe('console accessibility styles', () => {
  it('keeps a high-contrast keyboard focus indicator on form controls', () => {
    expect(styles).toContain('outline: 2px solid #2563eb;')
    expect(styles).toContain('outline-offset: 2px;')
  })

  it('uses readable contrast for compact sidebar labels', () => {
    expect(styles).not.toContain('color: #a1a1aa;')
  })

  it('defines a compact operations-console shell with stable dimensions', () => {
    expect(styles).toContain('--app-header-height: 48px;')
    expect(styles).toContain('--sidebar-width: 216px;')
    expect(styles).toContain('--sidebar-collapsed-width: 64px;')
    expect(styles).toContain('--sidebar-current-width: var(--sidebar-width);')
    expect(styles).toContain('--touch-target: 44px;')
    expect(ruleBody('.app-header')).toContain('height: var(--app-header-height);')
    expect(ruleBody('.desktop-sidebar')).toContain('width: var(--sidebar-current-width);')
    expect(ruleBody('.app-main')).toContain('margin-left: var(--sidebar-current-width);')
  })

  it('keeps build metadata in an unframed sidebar footer', () => {
    const metadata = ruleBody('.sidebar-meta')

    expect(metadata).toContain('border-top: 1px solid var(--line);')
    expect(metadata).not.toContain('border-radius')
    expect(metadata).not.toMatch(/background(?:-color)?:/)
    expect(metadata).not.toContain('box-shadow')
  })

  it('provides complete semantic colors and mobile touch targets without decoration', () => {
    for (const token of ['--success:', '--warning:', '--danger:', '--info:']) {
      expect(styles).toContain(token)
    }
    expect(styles).toContain('min-height: var(--touch-target);')
    expect(styles).not.toMatch(/(?:linear|radial)-gradient\(/)

    const letterSpacingValues = [...styles.matchAll(/letter-spacing:\s*([^;]+);/g)]
      .map((match) => match[1]?.trim())
    expect(new Set(letterSpacingValues)).toEqual(new Set(['0']))
  })
})
