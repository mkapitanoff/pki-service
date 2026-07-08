'use client'

import { useEffect, useState } from 'react'
import AdminGuard from '@/components/AdminGuard'
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
    <div className="p-8 max-w-6xl mx-auto">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Реестр подписаний</h1>
        <select
          className="border rounded px-3 py-2"
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

      {error && <div className="bg-red-50 text-red-600 p-3 rounded mb-4">{error}</div>}

      <div className="bg-white rounded-lg border overflow-hidden">
        <table className="w-full">
          <thead className="bg-gray-50">
            <tr>
              <th className="text-left p-4 font-medium">Документ</th>
              <th className="text-left p-4 font-medium">Заявка</th>
              <th className="text-left p-4 font-medium">Подписант</th>
              <th className="text-left p-4 font-medium">Статус</th>
              <th className="text-left p-4 font-medium">Подписан</th>
              <th className="text-left p-4 font-medium">Целостность</th>
              <th className="text-left p-4 font-medium">Действия</th>
            </tr>
          </thead>
          <tbody>
            {documents.map((d) => (
              <tr key={d.id} className="border-t">
                <td className="p-4 max-w-xs truncate" title={d.document_name}>{d.document_name}</td>
                <td className="p-4 text-sm text-gray-500">
                  {d.application_id?.Valid ? d.application_id.String : '—'}
                </td>
                <td className="p-4 text-sm">
                  {d.signer_name?.Valid ? d.signer_name.String : '—'}
                  {d.org_name?.Valid && (
                    <div className="text-xs text-gray-500">{d.org_name.String}</div>
                  )}
                </td>
                <td className="p-4">
                  <span className="px-2 py-1 rounded text-xs bg-gray-100 text-gray-700">
                    {STATUS_LABEL[d.status] ?? d.status}
                  </span>
                </td>
                <td className="p-4 text-sm text-gray-500">
                  {d.signed_at?.Valid ? new Date(d.signed_at.Time).toLocaleString('ru-RU') : '—'}
                </td>
                <td className="p-4 text-sm">
                  {d.verification_status?.Valid ? (
                    <span
                      className={
                        d.verification_status.String === 'verified'
                          ? 'text-green-600'
                          : d.verification_status.String === 'mismatch'
                            ? 'text-red-600 font-semibold'
                            : 'text-gray-500'
                      }
                    >
                      {d.verification_status.String}
                    </span>
                  ) : (
                    '—'
                  )}
                </td>
                <td className="p-4 space-x-3 whitespace-nowrap">
                  {d.status === 'signed' || d.status === 'uploaded' ? (
                    <button
                      onClick={() => handleDownload(d)}
                      disabled={downloadingId === d.id}
                      className="text-blue-600 hover:underline text-sm disabled:opacity-50"
                    >
                      {downloadingId === d.id ? 'Скачивание…' : 'Скачать'}
                    </button>
                  ) : null}
                  <a
                    href={`/verify/${d.id}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-blue-600 hover:underline text-sm"
                  >
                    Verify
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!loading && documents.length === 0 && (
          <div className="p-8 text-center text-gray-500">Документов нет</div>
        )}
        {loading && <div className="p-8 text-center text-gray-500">Загрузка...</div>}
      </div>

      {total > 0 && (
        <div className="flex justify-between items-center mt-4 text-sm text-gray-500">
          <span>{pageStart}–{pageEnd} из {total}</span>
          <div className="space-x-2">
            <button
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              disabled={offset === 0}
              className="border px-3 py-1 rounded disabled:opacity-50"
            >
              Назад
            </button>
            <button
              onClick={() => setOffset(offset + PAGE_SIZE)}
              disabled={offset + PAGE_SIZE >= total}
              className="border px-3 py-1 rounded disabled:opacity-50"
            >
              Вперёд
            </button>
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
