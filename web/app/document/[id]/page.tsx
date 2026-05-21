"use client";

import { use, useEffect, useState, useCallback } from "react"; // use is needed in DocumentPage
import Link from "next/link";
import {
  ArrowLeft,
  Loader2,
  AlertCircle,
  CheckCircle2,
  Clock,
  FileSignature,
  Download,
  QrCode,
} from "lucide-react";
import clsx from "clsx";
import { getDocument, API_BASE, Document, Signature, DocumentStatus } from "@/lib/api";
import { SignModal } from "./sign-modal";
import AuthGuard from "@/components/AuthGuard";

// ---- helpers ----

function nullStr(v: unknown): string {
  if (!v) return "";
  if (typeof v === "string") return v;
  if (typeof v === "object" && v !== null && "String" in v) return (v as { String: string }).String;
  return String(v);
}

function maskIIN(iin: unknown): string {
  const s = nullStr(iin);
  if (!s || s.length < 8) return s || "—";
  return s.slice(0, 4) + "****" + s.slice(-4);
}

function maskSerial(serial: string): string {
  if (serial.length <= 7) return serial;
  return serial.slice(0, 4) + "..." + serial.slice(-3);
}

function maskHash(hash: string): string {
  if (hash.length <= 16) return hash;
  return hash.slice(0, 8) + "..." + hash.slice(-8);
}

function fmtDate(iso: unknown): string {
  if (!iso) return "—";
  // Handle sqlc NullTime: { Time: "...", Valid: bool }
  const s =
    typeof iso === "object" && iso !== null && "Time" in iso
      ? (iso as { Time: string; Valid: boolean }).Valid
        ? (iso as { Time: string }).Time
        : null
      : (iso as string);
  if (!s) return "—";
  return new Date(s).toLocaleString("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const STATUS_CONFIG: Record<
  DocumentStatus,
  { label: string; color: string; icon: React.ReactNode }
> = {
  draft: {
    label: "Черновик",
    color: "bg-zinc-100 text-zinc-600",
    icon: <Clock className="w-3.5 h-3.5" />,
  },
  pending: {
    label: "Ожидает подписи",
    color: "bg-amber-50 text-[#fdcb6e] border border-amber-200",
    icon: <Clock className="w-3.5 h-3.5" />,
  },
  partially_signed: {
    label: "Частично подписан",
    color: "bg-blue-50 text-[#0070f3] border border-blue-200",
    icon: <FileSignature className="w-3.5 h-3.5" />,
  },
  signed: {
    label: "Подписан",
    color: "bg-green-50 text-[#00b894] border border-green-200",
    icon: <CheckCircle2 className="w-3.5 h-3.5" />,
  },
  rejected: {
    label: "Отклонён",
    color: "bg-red-50 text-[#d63031] border border-red-200",
    icon: <AlertCircle className="w-3.5 h-3.5" />,
  },
};

// ---- SignatureCard ----

function SignatureCard({ sig }: { sig: Signature }) {
  console.log("sha256:", sig.sha256_hash);
  const cfg = sig.ocsp_status === "good"
    ? { label: "Подпись действительна ✓", color: "text-[#00b894]" }
    : { label: "Статус неизвестен", color: "text-zinc-400" };

  return (
    <div className="border border-zinc-200 rounded-xl p-4 space-y-3 bg-white">
      <div className="flex items-start justify-between gap-2">
        <div>
          <p className="font-semibold text-zinc-900">{sig.signer_name}</p>
          <p className="text-xs text-zinc-400 mt-0.5">
            {sig.signer_type === "legal_entity_rep"
              ? "Представитель юридического лица"
              : "Физическое лицо"}
          </p>
        </div>
        <span className="text-xs font-medium text-zinc-400 shrink-0">
          #{sig.sequence_num}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
        {nullStr(sig.signer_iin) && (
          <>
            <span className="text-zinc-500">ИИН</span>
            <span className="font-mono">{maskIIN(sig.signer_iin)}</span>
          </>
        )}
        {nullStr(sig.org_name) && (
          <>
            <span className="text-zinc-500">Организация</span>
            <span className="truncate">{nullStr(sig.org_name)}</span>
          </>
        )}
        {nullStr(sig.signer_bin) && (
          <>
            <span className="text-zinc-500">БИН</span>
            <span className="font-mono">{nullStr(sig.signer_bin)}</span>
          </>
        )}
        {nullStr(sig.basis) && (
          <>
            <span className="text-zinc-500">Основание</span>
            <span>{nullStr(sig.basis)}</span>
          </>
        )}
        <span className="text-zinc-500">Роль</span>
        <span>{sig.role}</span>
        <span className="text-zinc-500">Дата подписи</span>
        <span>{fmtDate(sig.signed_at)}</span>
        {nullStr((sig.tsp_time as unknown as { Time: string; Valid: boolean })?.Time) && (
          <>
            <span className="text-zinc-500">Время TSP</span>
            <span>{fmtDate(sig.tsp_time)}</span>
          </>
        )}
      </div>

      <div className="border-t border-zinc-100 pt-3 space-y-1 text-xs text-zinc-500">
        <p>
          <span className="font-medium">УЦ:</span> {sig.ca_name}
        </p>
        <p>
          <span className="font-medium">№ сертификата:</span>{" "}
          {maskSerial(sig.cert_serial)}
        </p>
        <p>
          <span className="font-medium">Действителен:</span>{" "}
          {fmtDate(sig.cert_not_before)} — {fmtDate(sig.cert_not_after)}
        </p>
        <p>
          <span className="font-medium">Формат:</span> {sig.sign_format}
        </p>
        <p>
          <span className="font-medium">SHA-256:</span>{" "}
          <span className="font-mono">{maskHash(nullStr(sig.sha256_hash)) || "—"}</span>
        </p>
        <p className={clsx("font-medium", cfg.color)}>{cfg.label}</p>
      </div>

      <div className="flex items-center gap-3 pt-1">
        <QrCode className="w-10 h-10 text-zinc-300 shrink-0" />
        <a
          href={sig.qr_url}
          target="_blank"
          rel="noopener noreferrer"
          className="text-xs text-[#0070f3] hover:underline break-all"
        >
          {sig.qr_url}
        </a>
      </div>
    </div>
  );
}

// ---- Page ----

function DocumentPageInner({ id }: { id: string }) {
  const [doc, setDoc] = useState<Document | null>(null);
  const [docBase64, setDocBase64] = useState<string>("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [role, setRole] = useState("client");

  const load = useCallback(async () => {
    try {
      const d = await getDocument(id);
      setDoc(d);
      // Fetch PDF bytes for NCALayer signing
      fetch(`${API_BASE}/api/demo/download/${d.id}`)
        .then(r => r.arrayBuffer())
        .then(buf => {
          const bytes = new Uint8Array(buf);
          let b64 = "";
          for (let i = 0; i < bytes.length; i++) b64 += String.fromCharCode(bytes[i]);
          const encoded = btoa(b64);
          console.log("docBase64 loaded, length:", encoded.length);
          setDocBase64(encoded);
        })
        .catch(e => console.error("PDF fetch error:", e));
      setLoadError(null);
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "Ошибка загрузки");
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  if (loadError) {
    return (
      <main className="min-h-screen bg-zinc-50 flex items-center justify-center p-4">
        <div className="bg-white rounded-2xl border border-zinc-200 p-8 max-w-md w-full text-center space-y-4">
          <AlertCircle className="w-10 h-10 text-[#d63031] mx-auto" />
          <p className="text-zinc-700 font-medium">{loadError}</p>
          <Link
            href="/"
            className="inline-flex items-center gap-1 text-sm text-[#0070f3] hover:underline"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
            На главную
          </Link>
        </div>
      </main>
    );
  }

  if (!doc) {
    return (
      <main className="min-h-screen bg-zinc-50 flex items-center justify-center">
        <Loader2 className="w-8 h-8 text-[#0070f3] animate-spin" />
      </main>
    );
  }

  const statusCfg = STATUS_CONFIG[doc.status] ?? STATUS_CONFIG.draft;
  const sigs = doc.signatures ?? [];
  const currentHash =
    sigs.length > 0
      ? sigs[sigs.length - 1].sha256_hash
      : "";

  return (
    <main className="min-h-screen bg-zinc-50 py-8 px-4">
      <div className="max-w-2xl mx-auto space-y-6">
        {/* Back */}
        <Link
          href="/"
          className="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-800 transition-colors"
        >
          <ArrowLeft className="w-3.5 h-3.5" />
          Загрузить другой документ
        </Link>

        {/* Header card */}
        <div className="bg-white rounded-2xl border border-zinc-200 p-6">
          <div className="flex items-start justify-between gap-4 mb-4">
            <div className="min-w-0">
              <h1 className="text-xl font-bold text-zinc-900 truncate">
                {nullStr(doc.title) || "Без названия"}
              </h1>
              <p className="text-xs text-zinc-400 mt-1 font-mono break-all">
                ID: {doc.id}
              </p>
            </div>
            <span
              className={clsx(
                "flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold shrink-0",
                statusCfg.color
              )}
            >
              {statusCfg.icon}
              {statusCfg.label}
            </span>
          </div>

          <div className="flex flex-wrap gap-3 text-sm text-zinc-500 mb-5">
            <span>Версия: {doc.current_version}</span>
            <span>·</span>
            <span>Подписей: {sigs.length}</span>
            <span>·</span>
            <span>Обновлён: {fmtDate(doc.updated_at)}</span>
          </div>

          {/* Action buttons */}
          <div className="flex flex-wrap gap-2">
            <SignModal
              documentId={doc.id}
              documentTitle={nullStr(doc.title) || "Документ"}
              sha256Hash={currentHash}
              role={role}
              documentBase64={docBase64}
              onSigned={load}
            />

            {sigs.length > 0 && (
              <a
                href={`${API_BASE}/api/demo/download/${doc.id}`}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-2 px-4 py-2 rounded-lg border border-zinc-300 text-zinc-700 text-sm font-medium hover:border-zinc-400 transition-colors"
              >
                <Download className="w-4 h-4" />
                Скачать PDF
              </a>
            )}
          </div>

          {/* Role selector for signing */}
          <div className="mt-4 flex items-center gap-2">
            <span className="text-xs text-zinc-500">Роль:</span>
            {["client", "factor", "director"].map((r) => (
              <button
                key={r}
                type="button"
                onClick={() => setRole(r)}
                className={clsx(
                  "px-2.5 py-1 rounded-md text-xs font-medium border transition-colors",
                  role === r
                    ? "bg-[#0070f3] text-white border-[#0070f3]"
                    : "border-zinc-300 text-zinc-500 hover:border-zinc-400"
                )}
              >
                {r === "client"
                  ? "Клиент"
                  : r === "factor"
                    ? "Фактор"
                    : "Директор"}
              </button>
            ))}
          </div>
        </div>

        {/* Signatures */}
        <div>
          <h2 className="text-base font-semibold text-zinc-700 mb-3">
            Подписи ({sigs.length})
          </h2>
          {sigs.length === 0 ? (
            <div className="bg-white rounded-2xl border border-zinc-200 p-8 text-center text-zinc-400 text-sm">
              Документ ещё не подписан
            </div>
          ) : (
            <div className="space-y-3">
              {sigs.map((sig) => (
                <SignatureCard key={sig.id} sig={sig} />
              ))}
            </div>
          )}
        </div>
      </div>
    </main>
  );
}

export default function DocumentPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <AuthGuard>
      <DocumentPageInner id={id} />
    </AuthGuard>
  );
}
