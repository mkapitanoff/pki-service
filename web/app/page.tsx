"use client";

import { useState, useRef, DragEvent, ChangeEvent } from "react";
import { useRouter } from "next/navigation";
import { Upload, FileText, Loader2, AlertCircle, X, CheckCircle2, ExternalLink } from "lucide-react";
import clsx from "clsx";
import { demoUpload, ExistingSignatureInfo } from "@/lib/api";
import AuthGuard from "@/components/AuthGuard";

type FileStatus = "pending" | "uploading" | "done" | "deduplicated" | "error";

type FileEntry = {
  id: string;
  file: File;
  status: FileStatus;
  error?: string;
  documentId?: string;
  deduplicated?: boolean;
  existingSigs?: ExistingSignatureInfo[];
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

  // Add a deduplicated doc to batch and proceed to signing
  const addToBatch = (entry: FileEntry) => {
    const existing = JSON.parse(localStorage.getItem("pki_batch") ?? "[]") as object[];
    existing.push({
      document_id: entry.documentId!,
      title: entry.file.name.replace(/\.pdf$/i, ""),
      filename: entry.file.name,
      deduplicated: false,
    });
    localStorage.setItem("pki_batch", JSON.stringify(existing));
    router.push("/batch");
  };

  const uploadAll = async () => {
    if (!files.length) return;
    setGlobalError(null);
    setBusy(true);

    const batch: { document_id: string; title: string; filename: string; deduplicated: boolean }[] = [];

    for (const entry of files) {
      if (entry.status === "deduplicated" || entry.status === "done") continue;
      update(entry.id, { status: "uploading" });
      try {
        const title = entry.file.name.replace(/\.pdf$/i, "");
        const result = await demoUpload(entry.file, title);

        if (result.deduplicated) {
          update(entry.id, {
            status: "deduplicated",
            documentId: result.document_id,
            deduplicated: true,
            existingSigs: result.existing_signatures ?? [],
          });
        } else {
          update(entry.id, { status: "done", documentId: result.document_id });
          batch.push({ document_id: result.document_id, title, filename: entry.file.name, deduplicated: false });
        }
      } catch (e) {
        update(entry.id, {
          status: "error",
          error: e instanceof Error ? e.message : "Ошибка загрузки",
        });
      }
    }

    setBusy(false);

    // Redirect to batch only if there are non-deduplicated docs to sign
    if (batch.length > 0) {
      localStorage.setItem("pki_batch", JSON.stringify(batch));
      router.push("/batch");
    }
  };

  const pendingFiles = files.filter((f) => f.status === "pending");
  const canUpload = pendingFiles.length > 0 && !busy;

  return (
    <main className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-2xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-foreground">PKI Сервис</h1>
          <p className="text-muted-foreground mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-card rounded-2xl shadow-sm border border-border p-6 space-y-5">

          {/* Step indicator */}
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <span className="w-6 h-6 rounded-full bg-primary text-white flex items-center justify-center text-xs font-bold">1</span>
            <span className="font-medium text-foreground">Загрузка документов</span>
            <span className="mx-2 text-muted-foreground/60">→</span>
            <span className="w-6 h-6 rounded-full bg-muted text-muted-foreground flex items-center justify-center text-xs font-bold">2</span>
            <span>Подписание</span>
          </div>

          {/* Drop zone — hide when all files are processed */}
          {(files.length === 0 || pendingFiles.length > 0 || busy) && (
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
                  ? "border-primary bg-primary/10"
                  : files.length
                    ? "border-input bg-muted/40 hover:border-border"
                    : "border-input hover:border-border"
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
                <Upload className="w-10 h-10 text-muted-foreground" />
                <p className="text-muted-foreground font-medium">
                  Перетащите PDF-файлы или нажмите для выбора
                </p>
                <p className="text-sm text-muted-foreground">Несколько файлов, только .pdf</p>
              </div>
            </div>
          )}

          {/* File list */}
          {files.length > 0 && (
            <div className="space-y-3">
              {files.map((entry) => (
                <div key={entry.id}>
                  {/* File row */}
                  <div className={clsx(
                    "flex items-center gap-3 p-3 rounded-xl border",
                    entry.status === "deduplicated"
                      ? "border-warning/20 bg-warning/10"
                      : "border-border/50 bg-muted/40"
                  )}>
                    <FileText className={clsx(
                      "w-5 h-5 shrink-0",
                      entry.status === "deduplicated" ? "text-warning" : "text-muted-foreground"
                    )} />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-foreground truncate">{entry.file.name}</p>
                      <p className="text-xs text-muted-foreground">{(entry.file.size / 1024).toFixed(0)} КБ</p>
                    </div>

                    <span className={clsx(
                      "text-xs font-medium px-2 py-0.5 rounded-full shrink-0 flex items-center gap-1",
                      entry.status === "done"         && "bg-success/10 text-success",
                      entry.status === "error"        && "bg-destructive/10 text-destructive",
                      entry.status === "uploading"    && "bg-primary/10 text-primary",
                      entry.status === "pending"      && "bg-muted text-muted-foreground",
                      entry.status === "deduplicated" && "bg-warning/15 text-warning",
                    )}>
                      {entry.status === "uploading"    && <Loader2 className="w-3 h-3 animate-spin" />}
                      {entry.status === "done"         && <CheckCircle2 className="w-3 h-3" />}
                      {entry.status === "deduplicated" && <AlertCircle className="w-3 h-3" />}
                      {entry.status === "error"     ? entry.error
                        : entry.status === "done"   ? "Загружен"
                        : entry.status === "uploading" ? "Загрузка..."
                        : entry.status === "deduplicated" ? "Уже загружен"
                        : "Ожидает"}
                    </span>

                    {!busy && entry.status === "pending" && (
                      <button
                        onClick={(e) => { e.stopPropagation(); removeFile(entry.id); }}
                        className="shrink-0 text-muted-foreground/60 hover:text-destructive transition-colors"
                      >
                        <X className="w-4 h-4" />
                      </button>
                    )}
                  </div>

                  {/* Dedup warning panel */}
                  {entry.status === "deduplicated" && entry.documentId && (
                    <div className="mt-1 ml-2 p-3 bg-warning/10 border border-warning/20 rounded-xl text-sm space-y-2">
                      <p className="font-medium text-warning flex items-center gap-1.5">
                        <AlertCircle className="w-4 h-4 shrink-0" />
                        Этот документ уже был загружен ранее и содержит подписи:
                      </p>
                      {entry.existingSigs && entry.existingSigs.length > 0 ? (
                        <ul className="space-y-0.5 pl-5">
                          {entry.existingSigs.map((s, i) => (
                            <li key={i} className="text-xs text-warning">
                              {s.signer_name}{s.signer_iin ? ` (${s.signer_iin})` : ""} —{" "}
                              {s.valid ? "действительна" : "недействительна"}
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="text-xs text-warning pl-5">Подписи найдены в системе.</p>
                      )}
                      <p className="text-xs text-warning">Хотите добавить ещё одну подпись или открыть существующий документ?</p>
                      <div className="flex gap-2 pt-1">
                        <a
                          href={`/document/${entry.documentId}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-warning/30 text-warning text-xs font-medium hover:bg-warning/15 transition-colors"
                        >
                          <ExternalLink className="w-3 h-3" />
                          Открыть документ
                        </a>
                        <button
                          type="button"
                          onClick={() => addToBatch(entry)}
                          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary text-white text-xs font-medium hover:bg-[hsl(var(--primary-hover))] transition-colors"
                        >
                          <Upload className="w-3 h-3" />
                          Добавить подпись
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}

          {/* Global error */}
          {globalError && (
            <div className="flex items-start gap-2 bg-destructive/10 border border-destructive/20 rounded-lg p-3 text-sm text-destructive">
              <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
              <span>{globalError}</span>
            </div>
          )}

          {busy && (
            <div className="flex items-center gap-2 bg-primary/10 rounded-lg px-3 py-2 text-sm text-primary">
              <Loader2 className="w-4 h-4 animate-spin" />
              Загрузка файлов...
            </div>
          )}

          {canUpload && (
            <button
              type="button"
              onClick={uploadAll}
              disabled={!canUpload}
              className="w-full py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2 bg-primary hover:bg-[hsl(var(--primary-hover))]"
            >
              <Upload className="w-4 h-4" />
              {pendingFiles.length > 1
                ? `Загрузить документы (${pendingFiles.length})`
                : "Загрузить документ"}
            </button>
          )}

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
