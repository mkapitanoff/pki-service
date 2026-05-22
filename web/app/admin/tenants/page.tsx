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
  const [newType, setNewType] = useState('legal_entity')

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
      await adminCreateTenant(newName, newType)
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
          className="bg-blue-600 text-white px-4 py-2 rounded-lg hover:bg-blue-700"
        >
          + Создать тенанта
        </button>
      </div>

      {error && <div className="bg-red-50 text-red-600 p-3 rounded mb-4">{error}</div>}

      {showCreate && (
        <div className="bg-white border rounded-lg p-6 mb-6">
          <h2 className="font-semibold mb-4">Новый тенант</h2>
          <input
            className="border rounded px-3 py-2 w-full mb-3"
            placeholder="Название"
            value={newName}
            onChange={e => setNewName(e.target.value)}
          />
          <select
            className="border rounded px-3 py-2 w-full mb-3"
            value={newType}
            onChange={e => setNewType(e.target.value)}
          >
            <option value="legal_entity">Юридическое лицо</option>
            <option value="individual">Физическое лицо</option>
          </select>
          <div className="flex gap-2">
            <button onClick={createTenant} className="bg-blue-600 text-white px-4 py-2 rounded">Создать</button>
            <button onClick={() => setShowCreate(false)} className="border px-4 py-2 rounded">Отмена</button>
          </div>
        </div>
      )}

      <div className="bg-white rounded-lg border overflow-hidden">
        <table className="w-full">
          <thead className="bg-gray-50">
            <tr>
              <th className="text-left p-4 font-medium">Название</th>
              <th className="text-left p-4 font-medium">Тип</th>
              <th className="text-left p-4 font-medium">Статус</th>
              <th className="text-left p-4 font-medium">API ключей</th>
              <th className="text-left p-4 font-medium">Действия</th>
            </tr>
          </thead>
          <tbody>
            {tenants.map(t => (
              <tr key={t.id} className="border-t">
                <td className="p-4">{t.name}</td>
                <td className="p-4">{t.type === 'legal_entity' ? 'Юр. лицо' : 'Физ. лицо'}</td>
                <td className="p-4">
                  <span className={`px-2 py-1 rounded text-sm ${t.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'}`}>
                    {t.is_active ? 'Активен' : 'Неактивен'}
                  </span>
                </td>
                <td className="p-4">{t.api_keys_count}</td>
                <td className="p-4">
                  <button
                    onClick={() => router.push(`/admin/tenants/${t.id}/keys`)}
                    className="text-blue-600 hover:underline text-sm"
                  >
                    Ключи
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {tenants.length === 0 && (
          <div className="p-8 text-center text-gray-500">Тенантов нет</div>
        )}
      </div>
    </div>
  )
}
