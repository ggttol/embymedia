import { createRouter, createWebHistory } from 'vue-router'
import DashboardView from '../views/DashboardView.vue'
import ResourcesView from '../views/ResourcesView.vue'
import ResourceDetailView from '../views/ResourceDetailView.vue'
import FavoritesView from '../views/FavoritesView.vue'
import FilesView from '../views/FilesView.vue'
import TasksView from '../views/TasksView.vue'
import AgentsView from '../views/AgentsView.vue'
import SettingsView from '../views/SettingsView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'dashboard',
      component: DashboardView,
    },
    {
      path: '/resources',
      name: 'resources',
      component: ResourcesView,
    },
    {
      path: '/resources/:id',
      name: 'resource-detail',
      component: ResourceDetailView,
    },
    {
      path: '/favorites',
      name: 'favorites',
      component: FavoritesView,
    },
    {
      path: '/files',
      name: 'files',
      component: FilesView,
    },
    {
      path: '/tasks',
      name: 'tasks',
      component: TasksView,
    },
    {
      path: '/agents',
      name: 'agents',
      component: AgentsView,
    },
    {
      path: '/settings',
      name: 'settings',
      component: SettingsView,
    },
  ],
})

export default router
