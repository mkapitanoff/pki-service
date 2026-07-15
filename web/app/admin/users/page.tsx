'use client'

import { useEffect, useState } from 'react'
import { Plus, Trash2, UserRound } from 'lucide-react'
import {
  adminListUsers,
  adminUpdateUser,
  adminCreateUser,
  adminDeleteUser,
  adminListTenants,
  type AdminUser,
  type Tenant,
} from '@/lib/api'
import { Card } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

const ROLES = ['user', 'admin']

function fmtDate(v: { Time: string; Valid: boolean } | null | undefined): string {
  if (!v?.Valid) return '—'
  return new Date(v.Time).toLocaleDateString('ru-RU')
}

export default function UsersPage() {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminUser | null>(null)

  const [newEmail, setNewEmail] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newName, setNewName] = useState('')
  const [newRole, setNewRole] = useState('user')
  const [newTenantId, setNewTenantId] = useState('')
  const [createError, setCreateError] = useState('')
  const [createLoading, setCreateLoading] = useState(false)

  const [deleteError, setDeleteError] = useState('')
  const [deleteLoading, setDeleteLoading] = useState(false)

  useEffect(() => {
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

  return (
    <div className="p-6 sm:p-8 max-w-5xl mx-auto space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Пользователи</h1>
          <p className="text-sm text-muted-foreground mt-1">Роли и активация аккаунтов</p>
        </div>
        <Button onClick={() => setShowCreate(true)}>
          <Plus className="h-4 w-4" />
          <span className="hidden sm:inline">Добавить</span>
        </Button>
      </div>

      {error && (
        <div className="bg-destructive/10 text-destructive rounded-lg px-4 py-3 text-sm">{error}</div>
      )}

      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full data-table">
            <thead>
              <tr>
                <th>Пользователь</th>
                <th>Роль</th>
                <th>Статус</th>
                <th>Посл. вход</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {loading &&
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={5}>
                      <Skeleton className="h-5 w-full" />
                    </td>
                  </tr>
                ))}
              {!loading &&
                users.map((u) => {
                  const isActive = u.is_active?.Bool ?? true
                  return (
                    <tr key={u.id}>
                      <td>
                        <p className="font-medium text-foreground">{u.name}</p>
                        <p className="text-xs text-muted-foreground">{u.email}</p>
                      </td>
                      <td>
                        <select
                          value={u.role}
                          onChange={(e) => handleRoleChange(u, e.target.value)}
                          className="rounded-lg border border-input bg-background px-2 py-1 text-xs focus:border-primary focus:outline-none"
                        >
                          {ROLES.map((r) => (
                            <option key={r} value={r}>{r}</option>
                          ))}
                        </select>
                      </td>
                      <td>
                        <button onClick={() => handleToggleActive(u)} title="Переключить статус">
                          <Badge tone={isActive ? 'success' : 'muted'}>
                            {isActive ? 'Активен' : 'Неактивен'}
                          </Badge>
                        </button>
                      </td>
                      <td className="text-sm text-muted-foreground">{fmtDate(u.last_login_at)}</td>
                      <td className="text-right">
                        <button
                          onClick={() => setDeleteTarget(u)}
                          className="text-muted-foreground/60 hover:text-destructive transition-colors"
                          title="Удалить"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </td>
                    </tr>
                  )
                })}
            </tbody>
          </table>
        </div>
        {!loading && users.length === 0 && (
          <div className="py-12 text-center">
            <UserRound className="h-12 w-12 mx-auto mb-3 opacity-50 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">Пользователей пока нет</p>
          </div>
        )}
      </Card>

      {/* Create modal */}
      {showCreate && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <Card className="w-full max-w-md p-6 space-y-4">
            <h2 className="text-lg font-semibold text-foreground">Новый пользователь</h2>
            <form onSubmit={handleCreate} className="space-y-3">
              <div>
                <label className="mb-1 block text-sm text-muted-foreground">Имя</label>
                <input
                  className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="Иван Иванов"
                  required
                />
              </div>
              <div>
                <label className="mb-1 block text-sm text-muted-foreground">Email</label>
                <input
                  type="email"
                  className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
                  value={newEmail}
                  onChange={(e) => setNewEmail(e.target.value)}
                  placeholder="user@example.com"
                  required
                />
              </div>
              <div>
                <label className="mb-1 block text-sm text-muted-foreground">Пароль</label>
                <input
                  type="password"
                  className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder="Минимум 8 символов"
                  required
                  minLength={8}
                />
              </div>
              <div>
                <label className="mb-1 block text-sm text-muted-foreground">Роль</label>
                <select
                  className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                >
                  <option value="user">user</option>
                  <option value="admin">admin</option>
                </select>
              </div>
              <div>
                <label className="mb-1 block text-sm text-muted-foreground">Тенант (необязательно)</label>
                <select
                  className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
                  value={newTenantId}
                  onChange={(e) => setNewTenantId(e.target.value)}
                >
                  <option value="">Создать новый персональный тенант</option>
                  {tenants.map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </div>
              {createError && <p className="text-sm text-destructive">{createError}</p>}
              <div className="flex justify-end gap-2 pt-1">
                <Button type="button" variant="outline" onClick={() => setShowCreate(false)} disabled={createLoading}>
                  Отмена
                </Button>
                <Button type="submit" disabled={createLoading}>
                  {createLoading ? 'Создание…' : 'Создать'}
                </Button>
              </div>
            </form>
          </Card>
        </div>
      )}

      {/* Delete modal */}
      {deleteTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <Card className="w-full max-w-md p-6 space-y-4">
            <h2 className="text-lg font-semibold text-foreground">Удалить пользователя?</h2>
            <p className="text-sm text-muted-foreground">
              Удалить <span className="font-medium text-foreground">{deleteTarget.email}</span>? Это действие нельзя отменить.
            </p>
            {deleteError && <p className="text-sm text-destructive">{deleteError}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDeleteTarget(null)} disabled={deleteLoading}>
                Отмена
              </Button>
              <Button variant="destructive" onClick={handleDelete} disabled={deleteLoading}>
                {deleteLoading ? 'Удаление…' : 'Удалить'}
              </Button>
            </div>
          </Card>
        </div>
      )}
    </div>
  )
}
