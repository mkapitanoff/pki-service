'use client'

import { useEffect, useState } from 'react'
import { AlertCircle, ClipboardList, Download, ExternalLink } from 'lucide-react'
import AdminGuard from '@/components/AdminGuard'
import { Card, CardContent } from '@/components/ui/card'
import { Badge, type BadgeTone } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  adminListRegistry,
  adminDownloadRegistryDocument,
  type RegistryDocument,
} from '@/lib/api'

const PAGE_SIZE = 50

const STATUS_LABEL: Record<string, string> = {
  pending: 'Ожидает',
  ready: 'Готов',
  signing: 'Подписание',
  signed: 'Подписан',
  uploading: 'Загружается в S3',
  uploaded: 'Загружен в S3',
  fetch_failed: 'Ошибка получения',
  upload_failed: 'Ошибка загрузки',
}

const STATUS_TONE: Record<string, BadgeTone> = {
  pending: 'muted',
  ready: 'muted',
  signing: 'warning',
  signed: 'success',
  uploading: 'warning',
  uploaded: 'success',
  fetch_failed: 'destructive',
  upload_failed: 'destructive',
}

function StatusBadge({ status }: { status: string }) {
  return (
    <Badge tone={STATUS_TONE[status] ?? 'muted'}>
      {(STATUS_LABEL[status] ?? status).toUpperCase()}
    </Badge>
  )
}

function VerificationTag({ status }: { status: string }) {
  const tone: BadgeTone =
    status === 'verified' ? 'success' : status === 'mismatch' ? 'destructive' : 'muted'
  return <Badge tone={tone}>{status.toUpperCase()}</Badge>
}

function RegistryHome() {
  const [documents, setDocuments] = useState<RegistryDocument[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [statusFilter, setStatusFilter] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [downloadingId, setDownloadingId] = useState<string | null>(null)

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [offset, statusFilter])

  async function load() {
    try {
      setLoading(true)
      setError('')
      const data = await adminListRegistry({
        status: statusFilter || undefined,
        limit: PAGE_SIZE,
        offset,
      })
      setDocuments(data.documents ?? [])
      setTotal(data.total ?? 0)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }

  async function handleDownload(doc: RegistryDocument) {
    try {
      setDownloadingId(doc.id)
      const blob = await adminDownloadRegistryDocument(doc.id)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = doc.document_name
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка скачивания')
    } finally {
      setDownloadingId(null)
    }
  }

  const pageStart = offset + 1
  const pageEnd = Math.min(offset + PAGE_SIZE, total)

  return (
    <div className="max-w-6xl mx-auto p-6 sm:p-8 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Реестр подписаний</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Всего: {total}
          </p>
        </div>
        <select
          className="border border-border rounded-md px-3 py-2 text-sm bg-card text-foreground"
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value)
            setOffset(0)
          }}
        >
          <option value="">Все статусы</option>
          {Object.entries(STATUS_LABEL).map(([k, v]) => (
            <option key={k} value={k}>{v}</option>
          ))}
        </select>
      </div>

      {error && (
        <Card className="border-destructive/30">
          <CardContent className="pt-6 flex items-center gap-2 text-destructive text-sm">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {error}
          </CardContent>
        </Card>
      )}

      <Card className="overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full data-table">
            <thead>
              <tr>
                <th>Документ</th>
                <th>Заявка</th>
                <th>Подписант</th>
                <th>Статус</th>
                <th>Подписан</th>
                <th>Целостность</th>
                <th>Действия</th>
              </tr>
            </thead>
            <tbody>
              {loading &&
                Array.from({ length: 8 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={7} className="py-3 px-4">
                      <Skeleton className="h-5 w-full" />
                    </td>
                  </tr>
                ))}
              {!loading &&
                documents.map((d) => (
                  <tr key={d.id}>
                    <td className="max-w-xs truncate" title={d.document_name}>
                      {d.document_name}
                    </td>
                    <td className="text-sm text-muted-foreground">
                      {d.application_id?.Valid ? d.application_id.String : '—'}
                    </td>
                    <td className="text-sm">
                      {d.signer_name?.Valid ? d.signer_name.String : '—'}
                      {d.org_name?.Valid && (
                        <div className="text-xs text-muted-foreground">{d.org_name.String}</div>
                      )}
                    </td>
                    <td><StatusBadge status={d.status} /></td>
                    <td className="text-sm text-muted-foreground">
                      {d.signed_at?.Valid ? new Date(d.signed_at.Time).toLocaleString('ru-RU') : '—'}
                    </td>
                    <td className="text-sm">
                      {d.verification_status?.Valid ? (
                        <VerificationTag status={d.verification_status.String} />
                      ) : (
                        '—'
                      )}
                    </td>
                    <td className="whitespace-nowrap">
                      <div className="flex items-center gap-3">
                        {(d.status === 'signed' || d.status === 'uploaded') && (
                          <button
                            onClick={() => handleDownload(d)}
                            disabled={downloadingId === d.id}
                            className="inline-flex items-center gap-1 text-primary hover:underline font-medium text-sm disabled:opacity-50"
                          >
                            <Download className="h-3.5 w-3.5" />
                            {downloadingId === d.id ? 'Скачивание…' : 'Скачать'}
                          </button>
                        )}
                        <a
                          href={`/verify/${d.id}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-primary hover:underline font-medium text-sm"
                        >
                          <ExternalLink className="h-3.5 w-3.5" />
                          Verify
                        </a>
                      </div>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
        {!loading && documents.length === 0 && (
          <div className="py-12 text-center">
            <ClipboardList className="h-12 w-12 mx-auto mb-3 opacity-50 text-muted-foreground" />
            <p className="text-sm text-muted-foreground max-w-md mx-auto">
              Документов с таким фильтром не найдено
            </p>
          </div>
        )}
      </Card>

      {total > 0 && (
        <div className="flex justify-between items-center text-sm text-muted-foreground">
          <span>{pageStart}–{pageEnd} из {total}</span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              disabled={offset === 0}
            >
              Назад
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setOffset(offset + PAGE_SIZE)}
              disabled={offset + PAGE_SIZE >= total}
            >
              Вперёд
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

export default function RegistryPage() {
  return (
    <AdminGuard>
      <RegistryHome />
    </AdminGuard>
  )
}
