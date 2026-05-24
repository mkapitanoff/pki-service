"use client";

import { useState, useRef, DragEvent, ChangeEvent } from "react";
import { Upload, FileText, Loader2, AlertCircle, X, CheckCircle2, ExternalLink, Download } from "lucide-react";
import clsx from "clsx";
import { demoUpload, signDocument, getAuthToken, API_BASE } from "@/lib/api";
import { signMultiple } from "@/lib/ncalayer";
import AuthGuard from "@/components/AuthGuard";

type FileStatus =
  | "pending"
  | "uploading"
  | "uploaded"
  | "waiting_sign"
  | "signing"
  | "signed"
  | "error";

type FileEntry = {
  id: string; // local uuid
  file: File;
  status: FileStatus;
  error?: string;
  documentId?: string;
  base64?: string;
  role?: string; // role used when signing
};

type Phase = "select" | "uploading" | "signing" | "done" | "error";

const STATUS_LABEL: Record<FileStatus, string> = {
  pending:      "Ожидает загрузки",
  uploading:    "Загрузка...",
  uploaded:     "Загружен",
  waiting_sign: "Ожидает подписания",
  signing:      "Подписывается...",
  signed:       "Подписан",
  error:        "Ошибка",
};

const ROLE_OPTIONS = [
  { value: "client",      label: "Клиент" },
  { value: "factor",      label: "Фактор" },
  { value: "director",    label: "Директор" },
  { value: "accountant",  label: "Бухгалтер" },
  { value: "signatory",   label: "Уполномоченное лицо" },
];

function roleLabel(role: string): string {
  return ROLE_OPTIONS.find((o) => o.value === role)?.label ?? role;
}

function localId(): string {
  return Math.random().toString(36).slice(2);
}

function UploadPage() {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [phase, setPhase] = useState<Phase>("select");
  const [globalError, setGlobalError] = useState<string | null>(null);
  const [role, setRole] = useState("client");

  // ── File selection ─────────────────────────────────────────────
  const addFiles = (incoming: FileList | File[]) => {
    const pdfs = Array.from(incoming).filter((f) => f.type === "application/pdf");
    if (!pdfs.length) { setGlobalError("Допускаются только PDF-файлы"); return; }
    setGlobalError(null);
    setFiles((prev) => [
      ...prev,
      ...pdfs.map((f) => ({ id: localId(), file: f, status: "pending" as FileStatus })),
    ]);
  };

  const removeFile = (id: string) =>
    setFiles((prev) => prev.filter((f) => f.id !== id));

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragging(false);
    addFiles(e.dataTransfer.files);
  };

  const onFileChange = (e: ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) addFiles(e.target.files);
    e.target.value = "";
  };

  // ── Update single entry ────────────────────────────────────────
  const update = (id: string, patch: Partial<FileEntry>) =>
    setFiles((prev) => prev.map((f) => (f.id === id ? { ...f, ...patch } : f)));

  // ── Download signed PDF ────────────────────────────────────────
  const downloadPdf = async (documentId: string, fileName: string) => {
    const token = getAuthToken();
    const res = await fetch(`${API_BASE}/api/demo/download/${documentId}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) return;
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = fileName.replace(/\.pdf$/i, "") + "_signed.pdf";
    a.click();
    URL.revokeObjectURL(url);
  };

  // ── Main flow ──────────────────────────────────────────────────
  const run = async () => {
    if (!files.length) return;
    setGlobalError(null);

    // Step 1: Upload all files
    setPhase("uploading");
    const uploaded: FileEntry[] = [];

    for (const entry of files) {
      update(entry.id, { status: "uploading" });
      try {
        const result = await demoUpload(entry.file, entry.file.name.replace(/\.pdf$/i, ""));

        // Fetch base64 for signing
        const token = getAuthToken();
        const res = await fetch(`${API_BASE}/api/demo/download/${result.document_id}`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        });
        if (!res.ok) throw new Error("Ошибка скачивания файла для подписания");
        const buf = await res.arrayBuffer();
        const bytes = new Uint8Array(buf);
        let b64 = "";
        for (let i = 0; i < bytes.length; i++) b64 += String.fromCharCode(bytes[i]);
        const base64 = btoa(b64);

        update(entry.id, { status: "uploaded", documentId: result.document_id, base64 });
        uploaded.push({ ...entry, status: "uploaded", documentId: result.document_id, base64 });
      } catch (e) {
        update(entry.id, { status: "error", error: e instanceof Error ? e.message : "Ошибка" });
      }
    }

    if (!uploaded.length) {
      setPhase("error");
      setGlobalError("Ни один файл не был загружен успешно");
      return;
    }

    // Step 2: Sign all uploaded docs via NCALayer
    setPhase("signing");
    uploaded.forEach((e) => update(e.id, { status: "waiting_sign" }));

    let signatures: string[];
    try {
      const docs = uploaded.map((e) => e.base64!);
      signatures = await signMultiple(docs);
    } catch (e) {
      uploaded.forEach((en) => update(en.id, { status: "error", error: "NCALayer отменено" }));
      setPhase("error");
      setGlobalError(e instanceof Error ? e.message : "Ошибка NCALayer");
      return;
    }

    // Step 3: Submit each CMS to backend
    for (let i = 0; i < uploaded.length; i++) {
      const entry = uploaded[i];
      const cms = signatures[i];
      if (!cms) {
        update(entry.id, { status: "error", error: "Нет подписи" });
        continue;
      }
      update(entry.id, { status: "signing" });
      try {
        await signDocument(entry.documentId!, cms, role);
        update(entry.id, { status: "signed", role });
      } catch (e) {
        update(entry.id, { status: "error", error: e instanceof Error ? e.message : "Ошибка" });
      }
    }

    setPhase("done");
  };

  const reset = () => {
    setFiles([]);
    setPhase("select");
    setGlobalError(null);
  };

  const canStart = files.length > 0 && phase === "select";
  const busy = phase === "uploading" || phase === "signing";

  return (
    <main className="min-h-screen bg-zinc-50 flex items-center justify-center p-4">
      <div className="w-full max-w-2xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-zinc-900">PKI Сервис</h1>
          <p className="text-zinc-500 mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-white rounded-2xl shadow-sm border border-zinc-200 p-6 space-y-5">

          {/* Drop zone */}
          {phase === "select" && (
            <div
              role="button"
              tabIndex={0}
              onClick={() => inputRef.current?.click()}
              onKeyDown={(e) => e.key === "Enter" && inputRef.current?.click()}
              onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
              onDragLeave={() => setDragging(false)}
              onDrop={onDrop}
              className={clsx(
                "border-2 border-dashed rounded-xl p-8 text-center cursor-pointer transition-colors",
                dragging
                  ? "border-[#0070f3] bg-blue-50"
                  : files.length
                    ? "border-zinc-300 bg-zinc-50 hover:border-zinc-400"
                    : "border-zinc-300 hover:border-zinc-400"
              )}
            >
              <input
                ref={inputRef}
                type="file"
                accept="application/pdf"
                multiple
                className="hidden"
                onChange={onFileChange}
              />
              <div className="flex flex-col items-center gap-2">
                <Upload className="w-10 h-10 text-zinc-400" />
                <p className="text-zinc-600 font-medium">
                  Перетащите PDF-файлы или нажмите для выбора
                </p>
                <p className="text-sm text-zinc-400">Несколько файлов, только .pdf</p>
              </div>
            </div>
          )}

          {/* File list */}
          {files.length > 0 && (
            <div className="space-y-2">
              {files.map((entry) => (
                <div
                  key={entry.id}
                  className="flex items-center gap-3 p-3 rounded-xl border border-zinc-100 bg-zinc-50"
                >
                  <FileText className="w-5 h-5 text-zinc-400 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-zinc-800 truncate">{entry.file.name}</p>
                    <p className="text-xs text-zinc-400">
                      {(entry.file.size / 1024).toFixed(0)} КБ
                    </p>
                  </div>

                  {/* Status badge */}
                  <span className={clsx(
                    "text-xs font-medium px-2 py-0.5 rounded-full shrink-0 flex items-center gap-1",
                    entry.status === "signed"       && "bg-green-50 text-green-700",
                    entry.status === "error"        && "bg-red-50 text-red-600",
                    entry.status === "uploading"    && "bg-blue-50 text-blue-600",
                    entry.status === "signing"      && "bg-blue-50 text-blue-600",
                    entry.status === "uploaded"     && "bg-zinc-100 text-zinc-600",
                    entry.status === "waiting_sign" && "bg-amber-50 text-amber-600",
                    entry.status === "pending"      && "bg-zinc-100 text-zinc-500",
                  )}>
                    {(entry.status === "uploading" || entry.status === "signing") && (
                      <Loader2 className="w-3 h-3 animate-spin" />
                    )}
                    {entry.status === "signed" && <CheckCircle2 className="w-3 h-3" />}
                    {entry.status === "error"
                      ? entry.error
                      : entry.status === "signed" && entry.role
                        ? `Подписан (${roleLabel(entry.role)})`
                        : STATUS_LABEL[entry.status]}
                  </span>

                  {/* Open in new tab */}
                  {entry.status === "signed" && entry.documentId && (
                    <a
                      href={`/document/${entry.documentId}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="shrink-0 text-[#0070f3] hover:text-blue-800"
                      title="Открыть документ"
                    >
                      <ExternalLink className="w-4 h-4" />
                    </a>
                  )}

                  {/* Download signed PDF */}
                  {entry.status === "signed" && entry.documentId && (
                    <button
                      onClick={() => downloadPdf(entry.documentId!, entry.file.name)}
                      className="shrink-0 text-zinc-400 hover:text-zinc-700 transition-colors"
                      title="Скачать PDF"
                    >
                      <Download className="w-4 h-4" />
                    </button>
                  )}

                  {/* Remove button */}
                  {phase === "select" && (
                    <button
                      onClick={(e) => { e.stopPropagation(); removeFile(entry.id); }}
                      className="shrink-0 text-zinc-300 hover:text-red-500 transition-colors"
                    >
                      <X className="w-4 h-4" />
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* Global error */}
          {globalError && (
            <div className="flex items-start gap-2 bg-red-50 border border-red-200 rounded-lg p-3 text-sm text-[#d63031]">
              <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
              <span>{globalError}</span>
            </div>
          )}

          {/* Phase status */}
          {busy && (
            <div className="flex items-center gap-2 bg-blue-50 rounded-lg px-3 py-2 text-sm text-[#0070f3]">
              <Loader2 className="w-4 h-4 animate-spin" />
              {phase === "uploading" ? "Загрузка файлов..." : "Подписание через NCALayer..."}
            </div>
          )}

          {phase === "done" && (
            <div className="flex items-center gap-2 bg-green-50 rounded-lg px-3 py-2 text-sm text-green-700">
              <CheckCircle2 className="w-4 h-4" />
              Все документы обработаны. Нажмите на иконку для просмотра или скачивания.
            </div>
          )}

          {/* Role selector */}
          {(phase === "select" || phase === "error") && files.length > 0 && (
            <div className="flex items-center gap-3">
              <label className="text-sm text-zinc-600 shrink-0">Роль подписанта:</label>
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                className="flex-1 text-sm border border-zinc-300 rounded-lg px-3 py-2 bg-white text-zinc-800 focus:outline-none focus:ring-2 focus:ring-[#0070f3]"
              >
                {ROLE_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            </div>
          )}

          {/* Actions */}
          <div className="flex gap-3">
            {(phase === "select" || phase === "error") && (
              <button
                type="button"
                onClick={run}
                disabled={!canStart && phase !== "error"}
                className={clsx(
                  "flex-1 py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2",
                  files.length > 0
                    ? "bg-[#0070f3] hover:bg-blue-700"
                    : "bg-zinc-300 cursor-not-allowed"
                )}
              >
                <Upload className="w-4 h-4" />
                {files.length > 1
                  ? `Загрузить и подписать (${files.length} файла)`
                  : "Загрузить и подписать"}
              </button>
            )}

            {(phase === "done" || phase === "error") && (
              <button
                type="button"
                onClick={reset}
                className="flex-1 py-3 rounded-xl font-semibold text-zinc-700 border border-zinc-300 hover:border-zinc-400 transition-colors"
              >
                Загрузить ещё
              </button>
            )}
          </div>

        </div>
      </div>
    </main>
  );
}

export default function HomePage() {
  return (
    <AuthGuard>
      <UploadPage />
    </AuthGuard>
  );
}
