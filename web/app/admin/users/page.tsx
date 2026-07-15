'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminListUsers,
  adminUpdateUser,
  adminCreateUser,
  adminDeleteUser,
  adminListTenants,
  getAuthToken,
  type AdminUser,
  type Tenant,
} from '@/lib/api'

const ROLES = ['user', 'admin']

function fmtDate(v: { Time: string; Valid: boolean } | null | undefined): string {
  if (!v?.Valid) return '—'
  return new Date(v.Time).toLocaleDateString('ru-RU')
}

export default function UsersPage() {
  const router = useRouter()
  const [users, setUsers] = useState<AdminUser[]>([])
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminUser | null>(null)

  // Create form state
  const [newEmail, setNewEmail] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newName, setNewName] = useState('')
  const [newRole, setNewRole] = useState('user')
  const [newTenantId, setNewTenantId] = useState('')
  const [createError, setCreateError] = useState('')
  const [createLoading, setCreateLoading] = useState(false)

  // Delete modal state
  const [deleteError, setDeleteError] = useState('')
  const [deleteLoading, setDeleteLoading] = useState(false)

  useEffect(() => {
    const token = getAuthToken()
    if (!token) {
      router.push('/login')
      return
    }
    load()
  }, [])

  async function load() {
    try {
      setLoading(true)
      const [u, t] = await Promise.all([adminListUsers(), adminListTenants()])
      setUsers(u ?? [])
      setTenants(t ?? [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }

  async function handleRoleChange(user: AdminUser, role: string) {
    try {
      const isActive = user.is_active?.Bool ?? true
      await adminUpdateUser(user.id, role, isActive)
      load()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка обновления')
    }
  }

  async function handleToggleActive(user: AdminUser) {
    try {
      const isActive = user.is_active?.Bool ?? true
      await adminUpdateUser(user.id, user.role, !isActive)
      load()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка обновления')
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    if (!newEmail || !newPassword || !newName) return
    setCreateLoading(true)
    setCreateError('')
    try {
      await adminCreateUser(newEmail, newPassword, newName, newRole, newTenantId || undefined)
      setShowCreate(false)
      setNewEmail('')
      setNewPassword('')
      setNewName('')
      setNewRole('user')
      setNewTenantId('')
      load()
    } catch (e: unknown) {
      setCreateError(e instanceof Error ? e.message : 'Ошибка создания')
    } finally {
      setCreateLoading(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleteLoading(true)
    setDeleteError('')
    try {
      await adminDeleteUser(deleteTarget.id)
      setDeleteTarget(null)
      load()
    } catch (e: unknown) {
      setDeleteError(e instanceof Error ? e.message : 'Ошибка удаления')
    } finally {
      setDeleteLoading(false)
    }
  }

  if (loading) return <div className="p-8">Загрузка...</div>

  return (
    <div className="p-8 max-w-5xl mx-auto">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Пользователи</h1>
        <button
          onClick={() => setShowCreate(true)}
          className="bg-primary text-primary-foreground px-4 py-2 rounded-lg hover:opacity-90"
        >
          + Добавить
        </button>
      </div>

      {error && <div className="bg-destructive/10 text-destructive p-3 rounded mb-4">{error}</div>}

      {/* Create modal */}
      {showCreate && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-card rounded-lg border border-border p-6 w-full max-w-md space-y-4">
            <h2 className="text-lg font-semibold">Новый пользователь</h2>
            <form onSubmit={handleCreate} className="space-y-3">
              <div>
                <label className="block text-sm text-muted-foreground mb-1">Имя</label>
                <input
                  className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
                  value={newName}
                  onChange={e => setNewName(e.target.value)}
                  placeholder="Иван Иванов"
                  required
                />
              </div>
              <div>
                <label className="block text-sm text-muted-foreground mb-1">Email</label>
                <input
                  type="email"
                  className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
                  value={newEmail}
                  onChange={e => setNewEmail(e.target.value)}
                  placeholder="user@example.com"
                  required
                />
              </div>
              <div>
                <label className="block text-sm text-muted-foreground mb-1">Пароль</label>
                <input
                  type="password"
                  className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
                  value={newPassword}
                  onChange={e => setNewPassword(e.target.value)}
                  placeholder="Минимум 8 символов"
                  required
                  minLength={8}
                />
              </div>
              <div>
                <label className="block text-sm text-muted-foreground mb-1">Роль</label>
                <select
                  className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
                  value={newRole}
                  onChange={e => setNewRole(e.target.value)}
                >
                  <option value="user">user</option>
                  <option value="admin">admin</option>
                </select>
              </div>
              <div>
                <label className="block text-sm text-muted-foreground mb-1">Тенант (необязательно)</label>
                <select
                  className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
                  value={newTenantId}
                  onChange={e => setNewTenantId(e.target.value)}
                >
                  <option value="">Создать новый персональный тенант</option>
                  {tenants.map(t => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </div>
              {createError && <p className="text-sm text-destructive">{createError}</p>}
              <div className="flex gap-2 justify-end pt-1">
                <button
                  type="button"
                  onClick={() => setShowCreate(false)}
                  disabled={createLoading}
                  className="px-4 py-2 text-sm border border-border rounded-lg disabled:opacity-60"
                >
                  Отмена
                </button>
                <button
                  type="submit"
                  disabled={createLoading}
                  className="px-4 py-2 text-sm bg-primary text-primary-foreground rounded-lg hover:opacity-90 disabled:opacity-60"
                >
                  {createLoading ? 'Создание...' : 'Создать'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Delete modal */}
      {deleteTarget && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-card rounded-lg border border-border p-6 w-full max-w-md space-y-4">
            <h2 className="text-lg font-semibold">Удалить пользователя?</h2>
            <p className="text-sm text-muted-foreground">
              Удалить <span className="font-medium">{deleteTarget.email}</span>? Это действие нельзя отменить.
            </p>
            {deleteError && <p className="text-sm text-destructive">{deleteError}</p>}
            <div className="flex gap-2 justify-end">
              <button
                onClick={() => setDeleteTarget(null)}
                disabled={deleteLoading}
                className="px-4 py-2 text-sm border border-border rounded-lg disabled:opacity-60"
              >
                Отмена
              </button>
              <button
                onClick={handleDelete}
                disabled={deleteLoading}
                className="px-4 py-2 text-sm bg-destructive text-destructive-foreground rounded-lg hover:opacity-90 disabled:opacity-60"
              >
                {deleteLoading ? 'Удаление...' : 'Удалить'}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="bg-card rounded-lg border border-border overflow-hidden">
        <table className="w-full">
          <thead className="text-left">
            <tr>
              <th className="text-left p-4 font-medium text-xs uppercase tracking-wider text-muted-foreground">Пользователь</th>
              <th className="text-left p-4 font-medium text-xs uppercase tracking-wider text-muted-foreground">Роль</th>
              <th className="text-left p-4 font-medium text-xs uppercase tracking-wider text-muted-foreground">Статус</th>
              <th className="text-left p-4 font-medium text-xs uppercase tracking-wider text-muted-foreground">Посл. вход</th>
              <th className="text-left p-4 font-medium text-xs uppercase tracking-wider text-muted-foreground"></th>
            </tr>
          </thead>
          <tbody>
            {users.map(u => {
              const isActive = u.is_active?.Bool ?? true
              return (
                <tr key={u.id} className="border-t border-border/50">
                  <td className="p-4">
                    <p className="font-medium">{u.name}</p>
                    <p className="text-xs text-muted-foreground">{u.email}</p>
                    <p className="text-xs text-muted-foreground/70 font-mono">Создан: {fmtDate(u.created_at)}</p>
                  </td>
                  <td className="p-4">
                    <select
                      value={u.role}
                      onChange={e => handleRoleChange(u, e.target.value)}
                      className="text-xs border border-input rounded-lg px-2 py-1 bg-background focus:outline-none focus:border-primary"
                    >
                      {ROLES.map(r => (
                        <option key={r} value={r}>{r}</option>
                      ))}
                    </select>
                  </td>
                  <td className="p-4">
                    <button
                      onClick={() => handleToggleActive(u)}
                      className={`text-xs font-medium px-2 py-1 rounded-full border transition-colors ${
                        isActive
                          ? 'bg-success/15 text-success border-success/20 hover:bg-destructive/15 hover:text-destructive hover:border-destructive/20'
                          : 'bg-muted text-muted-foreground border-border hover:bg-success/15 hover:text-success hover:border-success/20'
                      }`}
                    >
                      {isActive ? 'Активен' : 'Неактивен'}
                    </button>
                  </td>
                  <td className="p-4 text-xs text-muted-foreground">{fmtDate(u.last_login_at)}</td>
                  <td className="p-4">
                    <button
                      onClick={() => setDeleteTarget(u)}
                      className="text-muted-foreground/60 hover:text-destructive transition-colors"
                      title="Удалить"
                    >
                      ✕
                    </button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
        {users.length === 0 && (
          <div className="p-8 text-center text-muted-foreground">Нет пользователей</div>
        )}
      </div>
    </div>
  )
}
