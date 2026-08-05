<script setup lang="ts">
import { RouterLink } from 'vue-router'

import { useConsoleStore } from '@/stores/console'

const store = useConsoleStore()
</script>

<template>
  <section class="page" aria-labelledby="upstreams-heading">
    <header class="page-header">
      <div>
        <h1 id="upstreams-heading">上游连接</h1>
      </div>
      <RouterLink class="secondary-button" to="/settings">管理连接配置</RouterLink>
    </header>

    <section class="workspace-panel route-panel" aria-label="上游连接列表">
      <div v-if="store.upstreams.length === 0" class="state-panel">
        <h2>尚未配置上游连接</h2>
        <RouterLink class="primary-button" to="/settings">前往系统设置</RouterLink>
      </div>
      <div v-else class="route-list">
        <article v-for="upstream in store.upstreams" :key="upstream.id" class="route-list-row">
          <div class="route-list-primary">
            <strong>{{ upstream.name }}</strong>
            <small>{{ upstream.id }} / {{ upstream.type }}</small>
          </div>
          <code>{{ upstream.base_url }}</code>
          <RouterLink
            class="secondary-button"
            :to="{ name: 'upstream-detail', params: { id: upstream.id } }"
            :aria-label="`查看 ${upstream.name} 详情`"
          >
            查看详情
          </RouterLink>
        </article>
      </div>
    </section>
  </section>
</template>
