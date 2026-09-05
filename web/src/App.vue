<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  LayoutDashboard,
  Search,
  Bookmark,
  FolderTree,
  ListTodo,
  Bot,
  Settings,
  Tv,
  Users,
  LogOut,
  UserRound,
  MoreHorizontal,
  RefreshCw,
} from 'lucide-vue-next'
import { useFavorites } from '@/stores/favorites'
import UiDialog from '@/components/UiDialog.vue'

const route = useRoute()
const router = useRouter()
const { count: favoritesCount } = useFavorites()

interface CurrentUser {
  username: string
  role: 'admin' | 'operator'
}

const currentUser = ref<CurrentUser | null>(null)
const showMore = ref(false)
const primaryPaths = ['/', '/resources', '/files', '/tasks']
const baseNavItems = [
  { name: '概览', desktopName: '仪表盘', path: '/', icon: LayoutDashboard },
  { name: '检索', desktopName: '资源检索', path: '/resources', icon: Search },
  { name: '收藏', desktopName: '我的收藏', path: '/favorites', icon: Bookmark, badge: () => favoritesCount.value },
  { name: '文件', desktopName: '115 文件', path: '/files', icon: FolderTree },
  { name: '任务', desktopName: '任务中心', path: '/tasks', icon: ListTodo },
  { name: '智能体', desktopName: '智能体接入', path: '/agent', icon: Bot },
  { name: '设置', desktopName: '系统设置', path: '/settings', icon: Settings },
]
const navItems = computed(() => [
  ...baseNavItems,
  ...(currentUser.value?.role === 'admin' ? [{ name: '用户', desktopName: '用户管理', path: '/users', icon: Users }] : []),
])
const primaryItems = computed(() => navItems.value.filter(item => primaryPaths.includes(item.path)))
const secondaryItems = computed(() => navItems.value.filter(item => !primaryPaths.includes(item.path)))
const moreActive = computed(() => secondaryItems.value.some(item => item.path === route.path))

const activeItem = computed(() =>
  navItems.value.find(item => item.path === route.path)
  ?? (route.path.startsWith('/resources/') ? navItems.value[1] : undefined)
)
const currentTitle = computed(() => activeItem.value?.desktopName ?? 'EmbyMedia')
const endpointLabel = window.location.host
const coreReachable = ref<boolean | null>(null)
const checkedAt = ref('')
const checkingCore = ref(false)
const coreLabel = computed(() => checkingCore.value ? '正在检测核心服务' : coreReachable.value === null ? '核心服务未检测' : coreReachable.value ? '核心服务可达' : '核心服务不可达')
const coreState = computed(() => checkingCore.value ? 'checking' : coreReachable.value === null ? 'untested' : coreReachable.value ? 'healthy' : 'failed')
const searchShortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K'

async function probeCore() {
  if (checkingCore.value) return
  checkingCore.value = true
  try {
    const response = await fetch('/api/v1/openapi.json', { cache: 'no-store' })
    coreReachable.value = response.ok && typeof (await response.json()).openapi === 'string'
  } catch {
    coreReachable.value = false
  } finally {
    checkedAt.value = new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
    checkingCore.value = false
  }
}

async function probeSession() {
  try {
    const response = await fetch('/auth/me', { cache: 'no-store' })
    if (response.status === 401) {
      window.location.assign(`/login?rd=${encodeURIComponent(window.location.pathname + window.location.search)}`)
      return
    }
    if (!response.ok) return
    const data = await response.json()
    currentUser.value = data.user ?? null
  } catch {
    currentUser.value = null
  }
}

async function focusGlobalSearch() {
  await router.push('/resources')
  await nextTick()
  document.querySelector<HTMLInputElement>('[data-global-search-input]')?.focus()
}

function openGlobalSearch(event: KeyboardEvent) {
  if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k' || document.querySelector('dialog[open]')) return
  event.preventDefault()
  void focusGlobalSearch()
}

watch(() => route.fullPath, () => {
  showMore.value = false
})

onMounted(() => {
  void Promise.all([probeCore(), probeSession()])
  window.addEventListener('keydown', openGlobalSearch)
})
onBeforeUnmount(() => window.removeEventListener('keydown', openGlobalSearch))
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

      <div class="rail-footer">
		<div v-if="currentUser" class="session-panel">
			<UserRound aria-hidden="true" />
			<span><strong>{{ currentUser.username }}</strong><small>{{ currentUser.role === 'admin' ? '管理员' : '操作员' }}</small></span>
			<a href="/logout" aria-label="退出登录" title="退出登录"><LogOut aria-hidden="true" /></a>
		</div>
      <div class="service-indicator">
        <span class="status-dot" :data-state="coreState" aria-hidden="true"></span>
        <span role="status">
          <strong>{{ coreLabel }}</strong>
          <small>{{ endpointLabel }}</small>
          <small v-if="checkedAt">检测于 {{ checkedAt }}</small>
        </span>
        <button type="button" class="status-refresh" aria-label="重新检测核心服务" :disabled="checkingCore" @click="probeCore"><RefreshCw aria-hidden="true" :class="{ 'animate-spin': checkingCore }" /></button>
      </div>
      </div>
    </aside>

    <div class="app-canvas">
      <header class="mobile-header">
        <RouterLink to="/" class="mobile-brand">
          <span class="brand-mark"><Tv aria-hidden="true" /></span>
          <strong>EmbyMedia</strong>
        </RouterLink>
		<div class="mobile-session"><span class="mobile-title">{{ currentTitle }}</span><a v-if="currentUser" href="/logout" aria-label="退出登录" title="退出登录"><LogOut aria-hidden="true" /></a></div>
      </header>

      <nav class="mobile-nav" aria-label="主导航">
        <RouterLink
          v-for="item in primaryItems"
          :key="item.path"
          :to="item.path"
          :class="{ active: activeItem?.path === item.path }"
        >
          <component :is="item.icon" aria-hidden="true" />
          <span>{{ item.name }}</span>
        </RouterLink>
        <button type="button" :class="{ active: moreActive }" aria-haspopup="dialog" :aria-expanded="showMore" @click="showMore = true"><MoreHorizontal aria-hidden="true" /><span>更多</span></button>
      </nav>

      <header class="desktop-header">
        <p class="header-location"><span>控制台</span><span aria-hidden="true">/</span><span>{{ currentTitle }}</span></p>
        <button type="button" class="global-search" @click="focusGlobalSearch">
          <Search aria-hidden="true" />
          <span>搜索资源</span>
          <kbd>{{ searchShortcut }}</kbd>
        </button>
      </header>

      <main class="view-canvas">
		<RouterView :key="route.fullPath" />
      </main>
    </div>
    <UiDialog v-if="showMore" title="更多功能" @close="showMore = false">
      <nav class="more-nav" aria-label="更多导航">
        <RouterLink v-for="item in secondaryItems" :key="item.path" :to="item.path" :class="{ active: activeItem?.path === item.path }" @click="showMore = false">
          <component :is="item.icon" aria-hidden="true" /><span>{{ item.desktopName }}</span>
          <span v-if="item.badge && item.badge() > 0" class="nav-badge">{{ item.badge() }}</span>
        </RouterLink>
      </nav>
      <div class="more-status"><span class="status-dot" :data-state="coreState" aria-hidden="true"></span><span role="status">{{ coreLabel }}<small v-if="checkedAt"> · {{ checkedAt }}</small></span><button class="status-refresh" type="button" :disabled="checkingCore" aria-label="重新检测核心服务" @click="probeCore"><RefreshCw aria-hidden="true" /></button></div>
    </UiDialog>
  </div>
</template>
