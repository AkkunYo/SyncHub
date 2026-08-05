<script setup lang="ts">
import { onMounted, onUnmounted, ref, useId } from 'vue'
import { X } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  title: string
  description?: string
  closeLabel?: string
  width?: 'narrow' | 'regular' | 'wide'
}>(), {
  description: undefined,
  closeLabel: '关闭抽屉',
  width: 'regular',
})

const emit = defineEmits<{
  close: []
}>()

const panel = ref<HTMLElement | null>(null)
const titleId = useId()
let previousFocus: HTMLElement | null = null
let previousOverflow = ''

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function close(): void {
  emit('close')
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusable = [...panel.value.querySelectorAll<HTMLElement>(focusableSelector)]
  if (focusable.length === 0) {
    event.preventDefault()
    panel.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || !panel.value.contains(active))) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first?.focus()
  }
}

onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  document.addEventListener('keydown', onKeydown)
  panel.value?.focus()
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <div class="side-panel-backdrop" @mousedown.self="close">
      <section
        ref="panel"
        class="side-panel"
        :class="`side-panel-${width}`"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        data-side="right"
        tabindex="-1"
      >
        <header class="side-panel-header">
          <div>
            <h2 :id="titleId">{{ title }}</h2>
            <p v-if="description">{{ description }}</p>
          </div>
          <button class="panel-close" type="button" :aria-label="props.closeLabel" :title="props.closeLabel" @click="close">
            <X :size="18" aria-hidden="true" />
          </button>
        </header>
        <div class="side-panel-body">
          <slot />
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.side-panel-backdrop {
  position: fixed;
  z-index: 120;
  inset: 0;
  display: flex;
  justify-content: flex-end;
  background: rgb(24 24 27 / 32%);
  backdrop-filter: blur(1px);
}

.side-panel {
  display: grid;
  width: min(100%, 520px);
  height: 100%;
  grid-template-rows: auto minmax(0, 1fr);
  border-left: 1px solid var(--line);
  color: var(--ink);
  background: var(--surface);
  box-shadow: -16px 0 40px rgb(15 23 42 / 14%);
  outline: none;
  animation: panel-enter 160ms ease-out;
}

.side-panel-narrow {
  width: min(100%, 420px);
}

.side-panel-wide {
  width: min(100%, 680px);
}

.side-panel-header {
  display: flex;
  min-height: 64px;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--line);
}

.side-panel-header h2 {
  margin: 0;
  font-size: 17px;
  line-height: 1.4;
}

.side-panel-header p {
  margin: 4px 0 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}

.panel-close {
  display: inline-grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--muted);
  background: transparent;
}

.panel-close:hover {
  border-color: var(--line);
  color: var(--ink);
  background: var(--surface-subtle);
}

.side-panel-body {
  min-height: 0;
  overflow-y: auto;
  padding: 18px;
}

@keyframes panel-enter {
  from { transform: translateX(20px); opacity: 0.75; }
  to { transform: translateX(0); opacity: 1; }
}

@media (max-width: 640px) {
  .side-panel-backdrop {
    align-items: flex-end;
  }

  .side-panel,
  .side-panel-narrow,
  .side-panel-wide {
    width: 100%;
    height: min(100%, 92dvh);
    border-top: 1px solid var(--line);
    border-left: 0;
    border-radius: 8px 8px 0 0;
    animation-name: panel-enter-mobile;
  }

  .side-panel-header,
  .side-panel-body {
    padding-right: 14px;
    padding-left: 14px;
  }
}

@keyframes panel-enter-mobile {
  from { transform: translateY(20px); opacity: 0.75; }
  to { transform: translateY(0); opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .side-panel {
    animation: none;
  }
}
</style>
