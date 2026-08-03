import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ApiError, api } from '@/api/client'
import type { BuildInfo, User } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const build = ref<BuildInfo | null>(null)
  const loading = ref(false)
  const checked = ref(false)

  /** Resolves the session cookie to a user, or leaves user null. */
  async function refresh(): Promise<void> {
    loading.value = true
    try {
      user.value = await api.get<User>('/api/v1/auth/me')
    } catch (err) {
      if (err instanceof ApiError && err.isUnauthorized) {
        user.value = null
      } else {
        throw err
      }
    } finally {
      loading.value = false
      checked.value = true
    }
  }

  /** Reads the backend build info, which the shell shows to prove it is connected. */
  async function loadBuild(): Promise<void> {
    build.value = await api.get<BuildInfo>('/version')
  }

  async function login(email: string, password: string): Promise<void> {
    await api.post('/api/v1/auth/login', { email, password })
    await refresh()
  }

  async function logout(): Promise<void> {
    await api.post('/api/v1/auth/logout')
    user.value = null
  }

  async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
    await api.post('/api/v1/auth/password', {
      current_password: currentPassword,
      new_password: newPassword,
    })
    await refresh()
  }

  return { user, build, loading, checked, refresh, loadBuild, login, logout, changePassword }
})
