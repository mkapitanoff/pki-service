"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, CheckCircle2, AlertCircle, ExternalLink, Download, QrCode } from "lucide-react";
import clsx from "clsx";
import { signDocument, getAuthToken, API_BASE } from "@/lib/api";
import { signMultiple } from "@/lib/ncalayer";
import AuthGuard from "@/components/AuthGuard";

type DocStatus =
  | "waiting"
  | "fetching"
  | "signing"
  | "submitting"
  | "signed"
  | "error";

type BatchItem = {
  document_id: string;
  title: string;
  filename: string;
  status: DocStatus;
  error?: string;
  signature_id?: string;
};

const ROLE_OPTIONS = [
  { value: "client",     label: "Клиент" },
  { value: "factor",     label: "Фактор" },
  { value: "director",   label: "Директор" },
  { value: "accountant", label: "Бухгалтер" },
  { value: "signatory",  label: "Уполномоченное лицо" },
];

const STATUS_LABEL: Record<DocStatus, string> = {
  waiting:    "Ожидает подписания",
  fetching:   "Загрузка PDF...",
  signing:    "Подписывается...",
  submitting: "Отправка подписи...",
  signed:     "Подписан",
  error:      "Ошибка",
};

async function fetchBase64(documentId: string): Promise<string> {
  const token = getAuthToken();
  const res = await fetch(`${API_BASE}/api/v1/documents/${documentId}/file`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const arrayBuffer = await res.arrayBuffer();
  return btoa(String.fromCharCode(...new Uint8Array(arrayBuffer)));
}

async function downloadFile(documentId: string, filename: string) {
  const token = getAuthToken();
  const res = await fetch(`${API_BASE}/api/v1/documents/${documentId}/file`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) return;
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename.replace(/\.pdf$/i, "") + "_signed.pdf";
  a.click();
  URL.revokeObjectURL(url);
}

function BatchPage() {
  const router = useRouter();
  const [items, setItems] = useState<BatchItem[]>([]);
  const [role, setRole] = useState("client");
  const [busy, setBusy] = useState(false);
  const [globalError, setGlobalError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  useEffect(() => {
    const raw = localStorage.getItem("pki_batch");
    if (!raw) { router.push("/"); return; }
    try {
      const batch = JSON.parse(raw) as { document_id: string; title: string; filename: string }[];
      setItems(batch.map((b) => ({ ...b, status: "waiting" })));
    } catch {
      router.push("/");
    }
  }, [router]);

  const update = (id: string, patch: Partial<BatchItem>) =>
    setItems((prev) => prev.map((it) => (it.document_id === id ? { ...it, ...patch } : it)));

  const signAll = async () => {
    if (busy || !items.length) return;
    setGlobalError(null);
    setBusy(true);

    // Step 1: fetch base64 for each doc
    const base64Map: Record<string, string> = {};
    for (const item of items) {
      update(item.document_id, { status: "fetching" });
      try {
        base64Map[item.document_id] = await fetchBase64(item.document_id);
      } catch (e) {
        update(item.document_id, {
          status: "error",
          error: e instanceof Error ? e.message : "Ошибка загрузки PDF",
        });
      }
    }

    const toSign = items.filter((it) => base64Map[it.document_id]);
    if (!toSign.length) {
      setGlobalError("Не удалось загрузить ни один документ");
      setBusy(false);
      return;
    }

    // Step 2: sign all via NCALayer (one password prompt)
    toSign.forEach((it) => update(it.document_id, { status: "signing" }));
    let signatures: string[];
    try {
      signatures = await signMultiple(toSign.map((it) => base64Map[it.document_id]));
    } catch (e) {
      toSign.forEach((it) => update(it.document_id, { status: "error", error: "NCALayer отменено" }));
      setGlobalError(e instanceof Error ? e.message : "Ошибка NCALayer");
      setBusy(false);
      return;
    }

    // Step 3: submit each CMS
    for (let i = 0; i < toSign.length; i++) {
      const item = toSign[i];
      const cms = signatures[i];
      if (!cms) { update(item.document_id, { status: "error", error: "Нет подписи" }); continue; }
      update(item.document_id, { status: "submitting" });
      try {
        const result = await signDocument(item.document_id, cms, role);
        update(item.document_id, { status: "signed", signature_id: result.signature_id });
      } catch (e) {
        update(item.document_id, {
          status: "error",
          error: e instanceof Error ? e.message : "Ошибка подписания",
        });
      }
    }

    setBusy(false);
    setDone(true);
  };

  const newBatch = () => {
    localStorage.removeItem("pki_batch");
    router.push("/");
  };

  const allSigned = items.length > 0 && items.every((it) => it.status === "signed");

  if (!items.length) {
    return (
      <main className="min-h-screen bg-zinc-50 flex items-center justify-center">
        <Loader2 className="w-6 h-6 animate-spin text-zinc-400" />
      </main>
    );
  }

  return (
    <main className="min-h-screen bg-zinc-50 flex items-center justify-center p-4">
      <div className="w-full max-w-3xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-zinc-900">PKI Сервис</h1>
          <p className="text-zinc-500 mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-white rounded-2xl shadow-sm border border-zinc-200 p-6 space-y-5">

          {/* Step indicator */}
          <div className="flex items-center gap-2 text-sm text-zinc-500">
            <span className="w-6 h-6 rounded-full bg-green-500 text-white flex items-center justify-center text-xs font-bold">✓</span>
            <span className="text-zinc-400">Загрузка документов</span>
            <span className="mx-2 text-zinc-300">→</span>
            <span className="w-6 h-6 rounded-full bg-[#0070f3] text-white flex items-center justify-center text-xs font-bold">2</span>
            <span className="font-medium text-zinc-700">Подписание ({items.length} {items.length === 1 ? "документ" : "документов"})</span>
          </div>

          {/* Documents table */}
          <div className="border border-zinc-200 rounded-xl overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-zinc-50 border-b border-zinc-200">
                <tr>
                  <th className="text-left px-4 py-3 text-zinc-600 font-medium">Файл</th>
                  <th className="text-left px-4 py-3 text-zinc-600 font-medium w-40">Статус</th>
                  {done && <th className="text-left px-4 py-3 text-zinc-600 font-medium w-40">Действия</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-100">
                {items.map((item) => (
                  <tr key={item.document_id} className="hover:bg-zinc-50">
                    <td className="px-4 py-3">
                      <p className="font-medium text-zinc-800 truncate max-w-xs">{item.filename}</p>
                      <p className="text-xs text-zinc-400 font-mono">{item.document_id.slice(0, 8)}…</p>
                    </td>
                    <td className="px-4 py-3">
                      <span className={clsx(
                        "inline-flex items-center gap-1.5 text-xs font-medium px-2 py-1 rounded-full",
                        item.status === "signed"    && "bg-green-50 text-green-700",
                        item.status === "error"     && "bg-red-50 text-red-600",
                        item.status === "waiting"   && "bg-zinc-100 text-zinc-500",
                        (item.status === "fetching" || item.status === "signing" || item.status === "submitting")
                          && "bg-blue-50 text-blue-600",
                      )}>
                        {(item.status === "fetching" || item.status === "signing" || item.status === "submitting") && (
                          <Loader2 className="w-3 h-3 animate-spin" />
                        )}
                        {item.status === "signed" && <CheckCircle2 className="w-3 h-3" />}
                        {item.status === "error"  && <AlertCircle className="w-3 h-3" />}
                        {item.status === "error" ? item.error : STATUS_LABEL[item.status]}
                      </span>
                    </td>
                    {done && (
                      <td className="px-4 py-3">
                        {item.status === "signed" && (
                          <div className="flex items-center gap-2">
                            <a
                              href={`/document/${item.document_id}`}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="text-[#0070f3] hover:text-blue-800"
                              title="Открыть документ"
                            >
                              <ExternalLink className="w-4 h-4" />
                            </a>
                            <button
                              onClick={() => downloadFile(item.document_id, item.filename)}
                              className="text-zinc-400 hover:text-zinc-700"
                              title="Скачать PDF"
                            >
                              <Download className="w-4 h-4" />
                            </button>
                            {item.signature_id && (
                              <a
                                href={`/verify/${item.signature_id}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-zinc-400 hover:text-zinc-700"
                                title="QR верификация"
                              >
                                <QrCode className="w-4 h-4" />
                              </a>
                            )}
                          </div>
                        )}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Global error */}
          {globalError && (
            <div className="flex items-start gap-2 bg-red-50 border border-red-200 rounded-lg p-3 text-sm text-[#d63031]">
              <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
              <span>{globalError}</span>
            </div>
          )}

          {allSigned && (
            <div className="flex items-center gap-2 bg-green-50 rounded-lg px-3 py-2 text-sm text-green-700">
              <CheckCircle2 className="w-4 h-4" />
              Все документы подписаны успешно.
            </div>
          )}

          {/* Role selector + action */}
          {!done && (
            <div className="flex items-center gap-3">
              <label className="text-sm text-zinc-600 shrink-0">Роль подписанта:</label>
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                disabled={busy}
                className="flex-1 text-sm border border-zinc-300 rounded-lg px-3 py-2 bg-white text-zinc-800 focus:outline-none focus:ring-2 focus:ring-[#0070f3] disabled:opacity-50"
              >
                {ROLE_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            </div>
          )}

          <div className="flex gap-3">
            {!done && (
              <button
                type="button"
                onClick={signAll}
                disabled={busy}
                className={clsx(
                  "flex-1 py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2",
                  busy ? "bg-zinc-300 cursor-not-allowed" : "bg-[#0070f3] hover:bg-blue-700"
                )}
              >
                {busy
                  ? <><Loader2 className="w-4 h-4 animate-spin" /> Подписание...</>
                  : `Подписать все через NCALayer (${items.length})`}
              </button>
            )}
            <button
              type="button"
              onClick={newBatch}
              className="px-6 py-3 rounded-xl font-semibold text-zinc-700 border border-zinc-300 hover:border-zinc-400 transition-colors"
            >
              Новая партия
            </button>
          </div>

        </div>
      </div>
    </main>
  );
}

export default function BatchSignPage() {
  return (
    <AuthGuard>
      <BatchPage />
    </AuthGuard>
  );
}
