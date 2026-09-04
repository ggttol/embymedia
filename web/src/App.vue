<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, RouterLink, RouterView } from 'vue-router'
import {
  LayoutDashboard,
  Search,
  Bookmark,
  FolderTree,
  ListTodo,
  Bot,
  Settings,
  Tv
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'

const route = useRoute()
const { count: favoritesCount } = useFavorites()

const navItems = [
  { name: '概览', desktopName: '仪表盘', path: '/', icon: LayoutDashboard },
  { name: '检索', desktopName: '资源检索', path: '/resources', icon: Search },
  { name: '收藏', desktopName: '我的收藏', path: '/favorites', icon: Bookmark, badge: () => favoritesCount.value },
  { name: '文件', desktopName: '115 文件', path: '/files', icon: FolderTree },
  { name: '任务', desktopName: '任务中心', path: '/tasks', icon: ListTodo },
  { name: '智能体', desktopName: '智能体接入', path: '/agents', icon: Bot },
  { name: '设置', desktopName: '系统设置', path: '/settings', icon: Settings },
]

const activeItem = computed(() =>
  navItems.find(item => item.path === route.path)
    ?? (route.path.startsWith('/resource/') ? navItems[1] : undefined)
)
const currentTitle = computed(() => activeItem.value?.desktopName ?? 'EmbyMedia')
const endpointLabel = window.location.host
</script>

<template>
  <div class="app-shell">
    <aside class="desktop-rail" aria-label="主导航">
      <RouterLink to="/" class="brand-lockup">
        <span class="brand-mark"><Tv aria-hidden="true" /></span>
        <span>
          <strong>EmbyMedia</strong>
          <small>MEDIA OPERATIONS</small>
        </span>
      </RouterLink>

      <nav class="desktop-nav">
        <RouterLink
          v-for="item in navItems"
          :key="item.path"
          :to="item.path"
          :class="{ active: activeItem?.path === item.path }"
        >
          <component :is="item.icon" aria-hidden="true" />
          <span>{{ item.desktopName }}</span>
          <span v-if="item.badge && item.badge() > 0" class="nav-badge">{{ item.badge() }}</span>
        </RouterLink>
      </nav>

      <div class="service-indicator">
        <span class="status-dot" aria-hidden="true"></span>
        <span>
          <strong>核心服务运行中</strong>
          <small>{{ endpointLabel }}</small>
        </span>
      </div>
    </aside>

    <div class="app-canvas">
      <header class="mobile-header">
        <RouterLink to="/" class="mobile-brand">
          <span class="brand-mark"><Tv aria-hidden="true" /></span>
          <strong>EmbyMedia</strong>
        </RouterLink>
        <span class="mobile-title">{{ currentTitle }}</span>
      </header>

      <nav class="mobile-nav" aria-label="主导航">
        <RouterLink
          v-for="item in navItems"
          :key="item.path"
          :to="item.path"
          :class="{ active: activeItem?.path === item.path }"
        >
          <component :is="item.icon" aria-hidden="true" />
          <span>{{ item.name }}</span>
        </RouterLink>
      </nav>

      <header class="desktop-header">
        <div>
          <span class="section-kicker">EMBYMEDIA / OPERATIONS</span>
          <h2>{{ currentTitle }}</h2>
        </div>
        <RouterLink to="/resources" class="global-search">
          <Search aria-hidden="true" />
          <span>搜索资源</span>
          <kbd>⌘ K</kbd>
        </RouterLink>
      </header>

      <main class="view-canvas">
        <RouterView />
      </main>
    </div>
  </div>
</template>
