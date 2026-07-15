'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { Building2, Plus, KeyRound } from 'lucide-react'
import { adminListTenants, adminCreateTenant } from '@/lib/api'
import { Card } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

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

  return (
    <div className="p-6 sm:p-8 max-w-5xl mx-auto space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Тенанты</h1>
          <p className="text-sm text-muted-foreground mt-1">Организации и их API-ключи</p>
        </div>
        <Button onClick={() => setShowCreate(true)}>
          <Plus className="h-4 w-4" />
          <span className="hidden sm:inline">Создать тенанта</span>
        </Button>
      </div>

      {error && (
        <div className="bg-destructive/10 text-destructive rounded-lg px-4 py-3 text-sm">{error}</div>
      )}

      {showCreate && (
        <Card className="p-5 space-y-3">
          <h2 className="font-semibold text-foreground">Новый тенант</h2>
          <input
            className="w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none"
            placeholder="Название организации"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            autoFocus
          />
          <div className="flex gap-2">
            <Button onClick={createTenant} disabled={!newName.trim()}>Создать</Button>
            <Button variant="outline" onClick={() => setShowCreate(false)}>Отмена</Button>
          </div>
        </Card>
      )}

      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full data-table">
            <thead>
              <tr>
                <th>Название</th>
                <th>Статус</th>
                <th className="text-right tabular-nums">API-ключей</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {loading &&
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={4}>
                      <Skeleton className="h-5 w-full" />
                    </td>
                  </tr>
                ))}
              {!loading &&
                tenants.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <div className="flex items-center gap-2 font-medium text-foreground">
                        <Building2 className="h-4 w-4 text-muted-foreground shrink-0" />
                        {t.name}
                      </div>
                    </td>
                    <td>
                      <Badge tone={t.is_active ? 'success' : 'muted'}>
                        {t.is_active ? 'Активен' : 'Неактивен'}
                      </Badge>
                    </td>
                    <td className="text-right tabular-nums text-muted-foreground">{t.api_keys_count}</td>
                    <td className="text-right whitespace-nowrap">
                      <button
                        onClick={() => router.push(`/admin/tenants/${t.id}/keys`)}
                        className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
                      >
                        <KeyRound className="h-3.5 w-3.5" />
                        Ключи
                      </button>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
        {!loading && tenants.length === 0 && (
          <div className="py-12 text-center">
            <Building2 className="h-12 w-12 mx-auto mb-3 opacity-50 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">Тенантов пока нет</p>
          </div>
        )}
      </Card>
    </div>
  )
}
