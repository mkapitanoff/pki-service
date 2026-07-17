'use client'

import { useEffect, useState } from 'react'
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  ClipboardList,
  Clock,
  Download,
  ExternalLink,
  Loader2,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge, type BadgeTone } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'
import { formatDateTime } from '@/lib/format'
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
  uploading: 'Загрузка в S3',
  uploaded: 'Загружен',
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

const STATUS_ICON: Record<string, typeof CheckCircle2> = {
  pending: Clock,
  ready: Clock,
  signing: Loader2,
  signed: CheckCircle2,
  uploading: Loader2,
  uploaded: CheckCircle2,
  fetch_failed: AlertTriangle,
  upload_failed: AlertTriangle,
}

function StatusBadge({ status }: { status: string }) {
  const Icon = STATUS_ICON[status] ?? Clock
  return (
    <Badge tone={STATUS_TONE[status] ?? 'muted'}>
      <Icon className="h-3 w-3" />
      {(STATUS_LABEL[status] ?? status).toUpperCase()}
    </Badge>
  )
}

function VerificationTag({ status }: { status: string }) {
  const tone: BadgeTone =
    status === 'verified' ? 'success' : status === 'mismatch' ? 'destructive' : 'muted'
  return <Badge tone={tone}>{status.toUpperCase()}</Badge>
}

// Quick-filters (§7): цвет фильтра совпадает с тоном статус-бейджа.
const FILTERS: { value: string; label: string; tone: BadgeTone }[] = [
  { value: '', label: 'Все', tone: 'muted' },
  { value: 'signed', label: 'Подписан', tone: 'success' },
  { value: 'uploaded', label: 'Загружен', tone: 'success' },
  { value: 'signing', label: 'Подписание', tone: 'warning' },
  { value: 'uploading', label: 'Загрузка', tone: 'warning' },
  { value: 'pending', label: 'Ожидает', tone: 'muted' },
  { value: 'fetch_failed', label: 'Ошибка получения', tone: 'destructive' },
  { value: 'upload_failed', label: 'Ошибка загрузки', tone: 'destructive' },
]

const FILTER_STYLES: Record<BadgeTone, { active: string; inactive: string }> = {
  success: { active: 'bg-success text-success-foreground', inactive: 'bg-success/10 text-success hover:bg-success/20' },
  warning: { active: 'bg-warning text-warning-foreground', inactive: 'bg-warning/10 text-warning hover:bg-warning/20' },
  destructive: { active: 'bg-destructive text-destructive-foreground', inactive: 'bg-destructive/10 text-destructive hover:bg-destructive/20' },
  info: { active: 'bg-primary text-primary-foreground', inactive: 'bg-primary/10 text-primary hover:bg-primary/20' },
  muted: { active: 'bg-foreground text-background', inactive: 'bg-muted text-muted-foreground hover:bg-muted/70' },
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
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold text-foreground">Реестр подписаний</h1>
        <p className="mt-1 text-sm text-muted-foreground">Всего: {total}</p>
      </div>

      {/* Quick-filters */}
      <div className="flex flex-wrap gap-2">
        {FILTERS.map((f) => {
          const active = statusFilter === f.value
          const s = FILTER_STYLES[f.tone]
          return (
            <button
              key={f.value || 'all'}
              onClick={() => {
                setStatusFilter(f.value)
                setOffset(0)
              }}
              className={cn(
                'rounded-full px-3 py-1 text-xs font-medium transition-colors',
                active ? s.active : s.inactive,
              )}
            >
              {f.label}
            </button>
          )
        })}
      </div>

      {error && (
        <Card className="border-destructive/30">
          <CardContent className="flex items-center gap-2 pt-6 text-sm text-destructive">
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
                    <td colSpan={7} className="px-4 py-3">
                      <Skeleton className="h-5 w-full" />
                    </td>
                  </tr>
                ))}
              {!loading &&
                documents.map((d) => {
                  const dt = d.signed_at?.Valid ? formatDateTime(d.signed_at.Time) : null
                  return (
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
                      <td>
                        <StatusBadge status={d.status} />
                      </td>
                      <td className="text-sm text-muted-foreground">
                        {dt ? (
                          <div>
                            <div className="tabular-nums text-foreground">{dt.date}</div>
                            <div className="text-xs tabular-nums">{dt.time}</div>
                          </div>
                        ) : (
                          '—'
                        )}
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
                              className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline disabled:opacity-50"
                            >
                              <Download className="h-3.5 w-3.5" />
                              {downloadingId === d.id ? 'Скачивание…' : 'Скачать'}
                            </button>
                          )}
                          <a
                            href={`/verify/${d.id}`}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
                          >
                            <ExternalLink className="h-3.5 w-3.5" />
                            Verify
                          </a>
                        </div>
                      </td>
                    </tr>
                  )
                })}
            </tbody>
          </table>
        </div>
        {!loading && documents.length === 0 && (
          <div className="py-12 text-center">
            <ClipboardList className="mx-auto mb-3 h-12 w-12 text-muted-foreground opacity-50" />
            <p className="mx-auto max-w-md text-sm text-muted-foreground">
              Документов с таким фильтром не найдено
            </p>
          </div>
        )}
      </Card>

      {total > 0 && (
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>
            {pageStart}–{pageEnd} из {total}
          </span>
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
  return <RegistryHome />
}
