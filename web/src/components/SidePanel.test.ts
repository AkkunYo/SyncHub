import { cleanup, render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SidePanel from './SidePanel.vue'

afterEach(() => {
  cleanup()
})

describe('SidePanel', () => {
  it('exposes an accessible right-side drawer and restores focus after Escape', async () => {
    const user = userEvent.setup()
    const trigger = document.createElement('button')
    trigger.textContent = '打开'
    document.body.append(trigger)
    trigger.focus()
    const onClose = vi.fn()
    const view = render(SidePanel, {
      props: { title: '编辑连接', closeLabel: '关闭编辑连接', onClose },
      slots: { default: '<button type="button">保存</button>' },
    })

    const dialog = screen.getByRole('dialog', { name: '编辑连接' })
    expect(dialog).toHaveClass('side-panel')
    expect(dialog).toHaveAttribute('data-side', 'right')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledOnce()

    view.unmount()
    expect(trigger).toHaveFocus()
    trigger.remove()
  })
})
