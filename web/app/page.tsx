"use client";

import { useState, useRef, DragEvent, ChangeEvent } from "react";
import { useRouter } from "next/navigation";
import { Upload, FileText, Loader2, AlertCircle } from "lucide-react";
import clsx from "clsx";
import { demoUpload } from "@/lib/api";
import AuthGuard from "@/components/AuthGuard";

export default function UploadPage() {
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);

  const [file, setFile] = useState<File | null>(null);
  const [title, setTitle] = useState("");
  const [dragging, setDragging] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const accept = (f: File) => {
    if (f.type !== "application/pdf") {
      setError("Допускаются только PDF-файлы");
      return;
    }
    setError(null);
    setFile(f);
    if (!title) setTitle(f.name.replace(/\.pdf$/i, ""));
  };

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragging(false);
    const f = e.dataTransfer.files[0];
    if (f) accept(f);
  };

  const onFileChange = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (f) accept(f);
  };

  const onSubmit = async () => {
    if (!file) return;
    setLoading(true);
    setError(null);
    try {
      const result = await demoUpload(file, title || file.name);
      router.push(`/document/${result.document_id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Неизвестная ошибка");
      setLoading(false);
    }
  };

  return (
    <AuthGuard>
    <main className="min-h-screen bg-zinc-50 flex items-center justify-center p-4">
      <div className="w-full max-w-lg">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-zinc-900">PKI Сервис</h1>
          <p className="text-zinc-500 mt-1">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="bg-white rounded-2xl shadow-sm border border-zinc-200 p-6 space-y-5">
          {/* Drop zone */}
          <div
            role="button"
            tabIndex={0}
            onClick={() => inputRef.current?.click()}
            onKeyDown={(e) => e.key === "Enter" && inputRef.current?.click()}
            onDragOver={(e) => {
              e.preventDefault();
              setDragging(true);
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={onDrop}
            className={clsx(
              "border-2 border-dashed rounded-xl p-10 text-center cursor-pointer transition-colors",
              dragging
                ? "border-[#0070f3] bg-blue-50"
                : file
                  ? "border-[#00b894] bg-green-50"
                  : "border-zinc-300 hover:border-zinc-400"
            )}
          >
            <input
              ref={inputRef}
              type="file"
              accept="application/pdf"
              className="hidden"
              onChange={onFileChange}
            />
            {file ? (
              <div className="flex flex-col items-center gap-2">
                <FileText className="w-10 h-10 text-[#00b894]" />
                <p className="font-medium text-zinc-800 break-all">{file.name}</p>
                <p className="text-sm text-zinc-400">
                  {(file.size / 1024).toFixed(0)} КБ
                </p>
              </div>
            ) : (
              <div className="flex flex-col items-center gap-2">
                <Upload className="w-10 h-10 text-zinc-400" />
                <p className="text-zinc-600 font-medium">
                  Перетащите PDF или нажмите для выбора
                </p>
                <p className="text-sm text-zinc-400">Только .pdf, до 20 МБ</p>
              </div>
            )}
          </div>

          {/* Title */}
          <div className="space-y-1">
            <label
              className="text-sm font-medium text-zinc-700"
              htmlFor="doc-title"
            >
              Название документа
            </label>
            <input
              id="doc-title"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Введите название..."
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-[#0070f3] focus:border-transparent"
            />
          </div>

          {/* Error */}
          {error && (
            <div className="flex items-start gap-2 bg-red-50 border border-red-200 rounded-lg p-3 text-sm text-[#d63031]">
              <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {/* Submit */}
          <button
            type="button"
            onClick={onSubmit}
            disabled={!file || loading}
            className={clsx(
              "w-full py-3 rounded-xl font-semibold text-white transition-colors flex items-center justify-center gap-2",
              file && !loading
                ? "bg-[#0070f3] hover:bg-blue-700"
                : "bg-zinc-300 cursor-not-allowed"
            )}
          >
            {loading ? (
              <>
                <Loader2 className="w-4 h-4 animate-spin" />
                Загрузка...
              </>
            ) : (
              <>
                <Upload className="w-4 h-4" />
                Загрузить документ
              </>
            )}
          </button>
        </div>
      </div>
    </main>
    </AuthGuard>
  );
}
