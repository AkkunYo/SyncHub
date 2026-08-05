<script setup lang="ts">
import { computed } from 'vue'
import { ArrowLeft, ArrowRight } from 'lucide-vue-next'
import { RouterLink, useRoute } from 'vue-router'

import { useConsoleStore } from '@/stores/console'

type ResourceKind = 'upstream' | 'target' | 'drift' | 'task'

const props = defineProps<{
  kind: ResourceKind
  title: string
  backTo: string
  backLabel: string
}>()

const route = useRoute()
const store = useConsoleStore()

const resourceId = computed(() => {
  const id = route.params.id
  return Array.isArray(id) ? (id[0] ?? '') : (id ?? '')
})

const configuredResource = computed(() => {
  if (props.kind === 'upstream') {
    return store.upstreams.find((upstream) => upstream.id === resourceId.value) ?? null
  }
  if (props.kind === 'target') {
    return store.targets.find((target) => target.id === resourceId.value) ?? null
  }
  return null
})

const isConfiguredKind = computed(() => props.kind === 'upstream' || props.kind === 'target')
const resourceMissing = computed(() => isConfiguredKind.value && !configuredResource.value)
const resourceTypeLabel = computed(() => (props.kind === 'task' ? '任务 ID' : props.kind === 'drift' ? '漂移 ID' : '实例 ID'))
const actionRoute = computed(() => {
  if (props.kind === 'upstream') return { name: 'sync' }
  if (props.kind === 'target') return { name: 'target-channels', params: { id: resourceId.value } }
  return null
})
const actionLabel = computed(() => (props.kind === 'target' ? '查看渠道' : '打开同步工作台'))
</script>

<template>
  <section class="page" aria-labelledby="resource-detail-heading">
    <header class="page-header">
      <div>
        <h1 id="resource-detail-heading">{{ title }}</h1>
        <p v-if="configuredResource" class="page-subtitle">{{ configuredResource.name }}</p>
      </div>
      <RouterLink class="secondary-button" :to="backTo">
        <ArrowLeft :size="16" aria-hidden="true" />
        {{ backLabel }}
      </RouterLink>
    </header>

    <section class="workspace-panel route-detail-panel" :aria-label="title">
      <div v-if="resourceMissing" class="state-panel">
        <h2>未找到对应实例</h2>
        <RouterLink class="primary-button" :to="backTo">返回列表</RouterLink>
      </div>
      <template v-else>
        <dl class="route-detail-summary" :class="{ 'is-compact': !configuredResource }">
          <div>
            <dt>{{ resourceTypeLabel }}</dt>
            <dd><code>{{ resourceId }}</code></dd>
          </div>
          <div v-if="configuredResource">
            <dt>类型</dt>
            <dd>{{ configuredResource.type }}</dd>
          </div>
          <div v-if="configuredResource">
            <dt>Base URL</dt>
            <dd><code>{{ configuredResource.base_url }}</code></dd>
          </div>
        </dl>
        <div v-if="actionRoute" class="route-detail-actions">
          <RouterLink class="primary-button" :to="actionRoute">
            {{ actionLabel }}
            <ArrowRight :size="16" aria-hidden="true" />
          </RouterLink>
        </div>
      </template>
    </section>
  </section>
</template>
