"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, CheckCircle2, AlertCircle, ExternalLink, Download, QrCode } from "lucide-react";
import clsx from "clsx";
import { signDocumentAsync, pollSignStatus, getAuthToken, API_BASE } from "@/lib/api";
import { signMultiple } from "@/lib/ncalayer";
import AuthGuard from "@/components/AuthGuard";

type DocStatus =
  | "waiting"
  | "already_signed"
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
  deduplicated?: boolean;
};

const ROLE_OPTIONS = [
  { value: "client",     label: "Клиент" },
  { value: "factor",     label: "Фактор" },
  { value: "director",   label: "Директор" },
  { value: "accountant", label: "Бухгалтер" },
  { value: "signatory",  label: "Уполномоченное лицо" },
];

const STATUS_LABEL: Record<DocStatus, string> = {
  waiting:       "Ожидает подписания",
  already_signed:"Уже подписан",
  fetching:      "Загрузка PDF...",
  signing:       "Подписывается...",
  submitting:    "Отправка подписи...",
  signed:        "Подписан",
  error:         "Ошибка",
};

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  const chunkSize = 8192;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

async function fetchBase64(documentId: string): Promise<string> {
  const token = getAuthToken();
  const res = await fetch(`${API_BASE}/api/v1/documents/${documentId}/file`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return arrayBufferToBase64(await res.arrayBuffer());
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
      const batch = JSON.parse(raw) as {
        document_id: string;
        title: string;
        filename: string;
        deduplicated?: boolean;
      }[];
      setItems(batch.map((b) => ({
        ...b,
        status: b.deduplicated ? "already_signed" : "waiting",
      })));
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

    // Exclude already_signed items — they are not sent to NCALayer
    const toProcess = items.filter((it) => it.status !== "already_signed");

    // Step 1: fetch base64
    const base64Map: Record<string, string> = {};
    for (const item of toProcess) {
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

    const toSign = toProcess.filter((it) => base64Map[it.document_id]);
    if (!toSign.length) {
      setGlobalError("Не удалось загрузить ни один документ для подписания");
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

    // Step 3: submit each CMS async, poll in parallel
    const pollPromises = toSign.map(async (item, i) => {
      const cms = signatures[i];
      if (!cms) { update(item.document_id, { status: "error", error: "Нет подписи" }); return; }
      update(item.document_id, { status: "submitting" });
      try {
        await signDocumentAsync(item.document_id, cms, role);
        update(item.document_id, { status: "signing" });
        const result = await pollSignStatus(item.document_id);
        update(item.document_id, { status: "signed", signature_id: result.signature_id });
      } catch (e) {
        update(item.document_id, {
          status: "error",
          error: e instanceof Error ? e.message : "Ошибка подписания",
        });
      }
    });

    await Promise.all(pollPromises);

    setBusy(false);
    setDone(true);
  };

  const newBatch = () => {
    localStorage.removeItem("pki_batch");
    router.push("/");
  };

  const signable = items.filter((it) => it.status !== "already_signed");
  const allSigned = signable.length > 0 && signable.every((it) => it.status === "signed");

  // Show actions column when done OR when there are already_signed items
  const showActions = done || items.some((it) => it.status === "already_signed");

  if (!items.length) {
    return (
      <main className="min-h-screen bg-background flex items-center justify-center">
        <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
      </main>
    );
  }

  return (
    <main className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-3xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-foreground">PKI Сервис</h1>
          <p className="text-muted-foreground mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-card rounded-2xl shadow-sm border border-border p-6 space-y-5">

          {/* Step indicator */}
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className="w-6 h-6 rounded-full bg-success text-white flex items-center justify-center text-xs font-bold">✓</span>
            <span className="text-muted-foreground">Загрузка документов</span>
            <span className="mx-2 text-muted-foreground/60">→</span>
            <span className="w-6 h-6 rounded-full bg-primary text-white flex items-center justify-center text-xs font-bold">2</span>
            <span className="font-medium text-foreground">
              Подписание ({signable.length} из {items.length})
            </span>
          </div>

          {/* Documents table */}
          <div className="border border-border rounded-xl overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/40 border-b border-border">
                <tr>
                  <th className="text-left px-4 py-3 text-muted-foreground font-medium">Файл</th>
                  <th className="text-left px-4 py-3 text-muted-foreground font-medium w-44">Статус</th>
                  {showActions && <th className="text-left px-4 py-3 text-muted-foreground font-medium w-32">Действия</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-border/50">
                {items.map((item) => (
                  <tr key={item.document_id} className={clsx(
                    "hover:bg-muted/40",
                    item.status === "already_signed" && "bg-warning/10"
                  )}>
                    <td className="px-4 py-3">
                      <p className="font-medium text-foreground truncate max-w-xs">{item.filename}</p>
                      <p className="text-xs text-muted-foreground font-mono">{item.document_id.slice(0, 8)}…</p>
                    </td>
                    <td className="px-4 py-3">
                      <span className={clsx(
                        "inline-flex items-center gap-1.5 text-xs font-medium px-2 py-1 rounded-full",
                        item.status === "signed"         && "bg-success/10 text-success",
                        item.status === "error"          && "bg-destructive/10 text-destructive",
                        item.status === "waiting"        && "bg-muted text-muted-foreground",
                        item.status === "already_signed" && "bg-warning/15 text-warning",
                        (item.status === "fetching" || item.status === "signing" || item.status === "submitting")
                          && "bg-primary/10 text-primary",
                      )}>
                        {(item.status === "fetching" || item.status === "signing" || item.status === "submitting") && (
                          <Loader2 className="w-3 h-3 animate-spin" />
                        )}
                        {item.status === "signed"         && <CheckCircle2 className="w-3 h-3" />}
                        {item.status === "already_signed" && <AlertCircle className="w-3 h-3" />}
                        {item.status === "error"          && <AlertCircle className="w-3 h-3" />}
                        {item.status === "error" ? item.error : STATUS_LABEL[item.status]}
                      </span>
                    </td>
                    {showActions && (
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          {(item.status === "signed" || item.status === "already_signed") && (
                            <a
                              href={`/document/${item.document_id}`}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="text-primary hover:text-primary"
                              title="Открыть документ"
                            >
                              <ExternalLink className="w-4 h-4" />
                            </a>
                          )}
                          {item.status === "signed" && (
                            <>
                              <button
                                onClick={() => downloadFile(item.document_id, item.filename)}
                                className="text-muted-foreground hover:text-foreground"
                                title="Скачать PDF"
                              >
                                <Download className="w-4 h-4" />
                              </button>
                              {item.signature_id && (
                                <a
                                  href={`/verify/${item.signature_id}`}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  className="text-muted-foreground hover:text-foreground"
                                  title="QR верификация"
                                >
                                  <QrCode className="w-4 h-4" />
                                </a>
                              )}
                            </>
                          )}
                        </div>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Global error */}
          {globalError && (
            <div className="flex items-start gap-2 bg-destructive/10 border border-destructive/20 rounded-lg p-3 text-sm text-destructive">
              <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
              <span>{globalError}</span>
            </div>
          )}

          {allSigned && (
            <div className="flex items-center gap-2 bg-success/10 rounded-lg px-3 py-2 text-sm text-success">
              <CheckCircle2 className="w-4 h-4" />
              Все документы подписаны успешно.
            </div>
          )}

          {/* Role selector + sign button */}
          {!done && signable.length > 0 && (
            <div className="flex items-center gap-3">
              <label className="text-sm text-muted-foreground shrink-0">Роль подписанта:</label>
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                disabled={busy}
                className="flex-1 text-sm border border-input rounded-lg px-3 py-2 bg-card text-foreground focus:outline-none focus:ring-2 focus:ring-primary disabled:opacity-50"
              >
                {ROLE_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            </div>
          )}

          <div className="flex gap-3">
            {!done && signable.length > 0 && (
              <button
                type="button"
                onClick={signAll}
                disabled={busy}
                className={clsx(
                  "flex-1 py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2",
                  busy ? "bg-muted cursor-not-allowed" : "bg-primary hover:bg-[hsl(var(--primary-hover))]"
                )}
              >
                {busy
                  ? <><Loader2 className="w-4 h-4 animate-spin" /> Подписание...</>
                  : `Подписать через NCALayer (${signable.length})`}
              </button>
            )}
            <button
              type="button"
              onClick={newBatch}
              className="px-6 py-3 rounded-xl font-semibold text-foreground border border-input hover:border-border transition-colors"
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
