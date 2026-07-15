'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminListTenants, adminCreateTenant, getAuthToken } from '@/lib/api'

interface Tenant {
  id: string
  name: string
  type: string
  is_active: boolean
  created_at: string
  api_keys_count: number
}

export default function TenantsPage() {
  const router = useRouter()
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [newName, setNewName] = useState('')

  useEffect(() => {
    const token = getAuthToken()
    if (!token) {
      router.push('/login')
      return
    }
    loadTenants()
  }, [])

  async function loadTenants() {
    try {
      setLoading(true)
      const data = await adminListTenants()
      setTenants(data ?? [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }

  async function createTenant() {
    try {
      await adminCreateTenant(newName, 'legal_entity')
      setShowCreate(false)
      setNewName('')
      loadTenants()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка создания')
    }
  }

  if (loading) return <div className="p-8">Загрузка...</div>

  return (
    <div className="p-8 max-w-5xl mx-auto">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Тенанты</h1>
        <button
          onClick={() => setShowCreate(true)}
          className="bg-primary text-primary-foreground px-4 py-2 rounded-lg hover:opacity-90"
        >
          + Создать тенанта
        </button>
      </div>

      {error && <div className="bg-destructive/10 text-destructive p-3 rounded mb-4">{error}</div>}

      {showCreate && (
        <div className="bg-card border border-border rounded-lg p-6 mb-6">
          <h2 className="font-semibold mb-4 text-foreground">Новый тенант</h2>
          <input
            className="border border-input rounded px-3 py-2 w-full mb-3 bg-background focus:outline-none focus:border-primary"
            placeholder="Название"
            value={newName}
            onChange={e => setNewName(e.target.value)}
          />
          <div className="flex gap-2">
            <button onClick={createTenant} className="bg-primary text-primary-foreground px-4 py-2 rounded hover:opacity-90">Создать</button>
            <button onClick={() => setShowCreate(false)} className="border border-border px-4 py-2 rounded text-muted-foreground">Отмена</button>
          </div>
        </div>
      )}

      <div className="bg-card rounded-lg border border-border overflow-hidden">
        <table className="w-full data-table">
          <thead>
            <tr>
              <th>Название</th>
              <th>Статус</th>
              <th>API ключей</th>
              <th>Действия</th>
            </tr>
          </thead>
          <tbody>
            {tenants.map(t => (
              <tr key={t.id}>
                <td>{t.name}</td>
                <td>
                  <span className={`px-2.5 py-1 rounded-full text-xs font-medium ${t.is_active ? 'bg-success/15 text-success' : 'bg-muted text-muted-foreground'}`}>
                    {t.is_active ? 'Активен' : 'Неактивен'}
                  </span>
                </td>
                <td>{t.api_keys_count}</td>
                <td>
                  <button
                    onClick={() => router.push(`/admin/tenants/${t.id}/keys`)}
                    className="text-primary hover:underline font-medium text-sm"
                  >
                    Ключи
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {tenants.length === 0 && (
          <div className="p-8 text-center text-muted-foreground">Тенантов нет</div>
        )}
      </div>
    </div>
  )
}
