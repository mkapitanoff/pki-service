"use client";

import { useState, useRef, DragEvent, ChangeEvent } from "react";
import { useRouter } from "next/navigation";
import { Upload, FileText, Loader2, AlertCircle, X, CheckCircle2 } from "lucide-react";
import clsx from "clsx";
import { demoUpload, getAuthToken, API_BASE, ExistingSignatureInfo } from "@/lib/api";
import AuthGuard from "@/components/AuthGuard";

type FileStatus = "pending" | "uploading" | "done" | "error";

type FileEntry = {
  id: string;
  file: File;
  status: FileStatus;
  error?: string;
  documentId?: string;
};

function localId(): string {
  return Math.random().toString(36).slice(2);
}

function UploadPage() {
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [busy, setBusy] = useState(false);
  const [globalError, setGlobalError] = useState<string | null>(null);
  const [existingSignsWarning, setExistingSignsWarning] = useState<ExistingSignatureInfo[]>([]);

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

  const update = (id: string, patch: Partial<FileEntry>) =>
    setFiles((prev) => prev.map((f) => (f.id === id ? { ...f, ...patch } : f)));

  const uploadAll = async () => {
    if (!files.length) return;
    setGlobalError(null);
    setBusy(true);

    const batch: { document_id: string; title: string; filename: string }[] = [];
    const allExistingSigs: ExistingSignatureInfo[] = [];

    for (const entry of files) {
      update(entry.id, { status: "uploading" });
      try {
        const title = entry.file.name.replace(/\.pdf$/i, "");
        const result = await demoUpload(entry.file, title);
        update(entry.id, { status: "done", documentId: result.document_id });
        batch.push({ document_id: result.document_id, title, filename: entry.file.name });
        if (result.existing_signatures?.length) {
          allExistingSigs.push(...result.existing_signatures);
        }
      } catch (e) {
        update(entry.id, {
          status: "error",
          error: e instanceof Error ? e.message : "Ошибка загрузки",
        });
      }
    }

    setBusy(false);

    if (!batch.length) {
      setGlobalError("Ни один файл не был загружен успешно");
      return;
    }

    if (allExistingSigs.length > 0) {
      setExistingSignsWarning(allExistingSigs);
    }

    localStorage.setItem("pki_batch", JSON.stringify(batch));
    router.push("/batch");
  };

  const canUpload = files.length > 0 && !busy;

  return (
    <main className="min-h-screen bg-zinc-50 flex items-center justify-center p-4">
      <div className="w-full max-w-2xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-zinc-900">PKI Сервис</h1>
          <p className="text-zinc-500 mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-white rounded-2xl shadow-sm border border-zinc-200 p-6 space-y-5">

          {/* Step indicator */}
          <div className="flex items-center gap-2 text-sm text-zinc-500">
            <span className="w-6 h-6 rounded-full bg-[#0070f3] text-white flex items-center justify-center text-xs font-bold">1</span>
            <span className="font-medium text-zinc-700">Загрузка документов</span>
            <span className="mx-2 text-zinc-300">→</span>
            <span className="w-6 h-6 rounded-full bg-zinc-200 text-zinc-500 flex items-center justify-center text-xs font-bold">2</span>
            <span>Подписание</span>
          </div>

          {/* Drop zone */}
          <div
            role="button"
            tabIndex={0}
            onClick={() => !busy && inputRef.current?.click()}
            onKeyDown={(e) => e.key === "Enter" && !busy && inputRef.current?.click()}
            onDragOver={(e) => { e.preventDefault(); if (!busy) setDragging(true); }}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => { if (!busy) onDrop(e); }}
            className={clsx(
              "border-2 border-dashed rounded-xl p-8 text-center transition-colors",
              busy ? "opacity-50 cursor-not-allowed" : "cursor-pointer",
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
                    <p className="text-xs text-zinc-400">{(entry.file.size / 1024).toFixed(0)} КБ</p>
                  </div>

                  <span className={clsx(
                    "text-xs font-medium px-2 py-0.5 rounded-full shrink-0 flex items-center gap-1",
                    entry.status === "done"      && "bg-green-50 text-green-700",
                    entry.status === "error"     && "bg-red-50 text-red-600",
                    entry.status === "uploading" && "bg-blue-50 text-blue-600",
                    entry.status === "pending"   && "bg-zinc-100 text-zinc-500",
                  )}>
                    {entry.status === "uploading" && <Loader2 className="w-3 h-3 animate-spin" />}
                    {entry.status === "done"      && <CheckCircle2 className="w-3 h-3" />}
                    {entry.status === "error"     ? entry.error
                      : entry.status === "done"   ? "Загружен"
                      : entry.status === "uploading" ? "Загрузка..."
                      : "Ожидает"}
                  </span>

                  {!busy && entry.status === "pending" && (
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

          {/* Existing signatures warning */}
          {existingSignsWarning.length > 0 && (
            <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 text-sm text-amber-800 space-y-1">
              <div className="flex items-center gap-2 font-medium">
                <AlertCircle className="w-4 h-4 shrink-0" />
                Документ уже содержит подписи от других сторон. Они будут сохранены.
              </div>
              {existingSignsWarning.map((s, i) => (
                <div key={i} className="text-xs text-amber-700 pl-6">
                  {s.signer_name} {s.signer_iin ? `(${s.signer_iin})` : ""} — {s.valid ? "действительна" : "недействительна"}
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

          {busy && (
            <div className="flex items-center gap-2 bg-blue-50 rounded-lg px-3 py-2 text-sm text-[#0070f3]">
              <Loader2 className="w-4 h-4 animate-spin" />
              Загрузка файлов...
            </div>
          )}

          <button
            type="button"
            onClick={uploadAll}
            disabled={!canUpload}
            className={clsx(
              "w-full py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2",
              canUpload ? "bg-[#0070f3] hover:bg-blue-700" : "bg-zinc-300 cursor-not-allowed"
            )}
          >
            <Upload className="w-4 h-4" />
            {files.length > 1
              ? `Загрузить документы (${files.length})`
              : "Загрузить документ"}
          </button>

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
