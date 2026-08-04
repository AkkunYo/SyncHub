<script setup lang="ts">
import { RouterLink } from 'vue-router'

import { useConsoleStore } from '@/stores/console'

const store = useConsoleStore()
</script>

<template>
  <section class="page" aria-labelledby="targets-heading">
    <header class="page-header">
      <div>
        <h1 id="targets-heading">目标实例</h1>
      </div>
      <RouterLink class="secondary-button" to="/settings">管理目标配置</RouterLink>
    </header>

    <section class="workspace-panel route-panel" aria-label="目标实例列表">
      <div v-if="store.targets.length === 0" class="state-panel">
        <h2>尚未配置目标实例</h2>
        <RouterLink class="primary-button" to="/settings">前往系统设置</RouterLink>
      </div>
      <div v-else class="route-list">
        <article v-for="target in store.targets" :key="target.id" class="route-list-row">
          <div class="route-list-primary">
            <strong>{{ target.name }}</strong>
            <small>{{ target.id }} / {{ target.type }}</small>
          </div>
          <code>{{ target.base_url }}</code>
          <RouterLink
            class="secondary-button"
            :to="{ name: 'target-channels', params: { id: target.id } }"
            :aria-label="`查看 ${target.name} 渠道`"
          >
            查看渠道
          </RouterLink>
        </article>
      </div>
    </section>
  </section>
</template>
