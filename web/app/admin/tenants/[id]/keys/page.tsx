"use client";

import { use, useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { ArrowLeft, Plus, Loader2, AlertCircle, Trash2, Copy, CheckCheck } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";
import {
  adminListKeys,
  adminCreateKey,
  adminDeactivateKey,
  type ApiKey,
} from "@/lib/api";

function fmtDate(v: { Time: string; Valid: boolean } | null | undefined): string {
  if (!v?.Valid) return "—";
  return new Date(v.Time).toLocaleDateString("ru-RU");
}

function KeyRow({
  apiKey,
  tenantId,
  onDeactivated,
}: {
  apiKey: ApiKey;
  tenantId: string;
  onDeactivated: () => void;
}) {
  const [loading, setLoading] = useState(false);

  const handleDeactivate = async () => {
    if (!confirm("Деактивировать ключ?")) return;
    setLoading(true);
    try {
      await adminDeactivateKey(tenantId, apiKey.id);
      onDeactivated();
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-between py-3 border-b border-zinc-100 last:border-0">
      <div>
        <p className="font-medium text-zinc-900">{apiKey.label}</p>
        <p className="text-xs text-zinc-400 font-mono mt-0.5">{apiKey.id}</p>
        <p className="text-xs text-zinc-400 mt-0.5">
          Истекает: {fmtDate(apiKey.expires_at)}
        </p>
      </div>
      <div className="flex items-center gap-3">
        <span
          className={`text-xs font-medium px-2 py-0.5 rounded-full ${
            apiKey.is_active
              ? "bg-green-50 text-green-700"
              : "bg-zinc-100 text-zinc-400"
          }`}
        >
          {apiKey.is_active ? "Активен" : "Неактивен"}
        </span>
        {apiKey.is_active && (
          <button
            onClick={handleDeactivate}
            disabled={loading}
            className="text-zinc-400 hover:text-[#d63031] transition-colors"
            title="Деактивировать"
          >
            {loading ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Trash2 className="w-4 h-4" />
            )}
          </button>
        )}
      </div>
    </div>
  );
}

function NewKeyModal({
  tenantId,
  onClose,
  onCreated,
}: {
  tenantId: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [label, setLabel] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!label.trim()) return;
    setLoading(true);
    setError(null);
    try {
      const result = await adminCreateKey(
        tenantId,
        label.trim(),
        expiresAt || undefined
      );
      setCreatedKey(result.key);
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Ошибка");
    } finally {
      setLoading(false);
    }
  };

  const handleCopy = () => {
    if (!createdKey) return;
    navigator.clipboard.writeText(createdKey).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  if (createdKey) {
    return (
      <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
        <div className="bg-white rounded-2xl border border-zinc-200 p-6 w-full max-w-md space-y-4">
          <h2 className="text-lg font-semibold text-zinc-900">Ключ создан</h2>
          <p className="text-sm text-zinc-600">
            Сохраните ключ — он отображается только один раз.
          </p>
          <div className="bg-zinc-50 border border-zinc-200 rounded-lg p-3 flex items-center gap-2">
            <code className="text-xs font-mono break-all flex-1 text-zinc-800">
              {createdKey}
            </code>
            <button onClick={handleCopy} className="shrink-0 text-zinc-400 hover:text-zinc-700">
              {copied ? (
                <CheckCheck className="w-4 h-4 text-green-600" />
              ) : (
                <Copy className="w-4 h-4" />
              )}
            </button>
          </div>
          <div className="flex justify-end">
            <button
              onClick={onClose}
              className="px-4 py-2 text-sm bg-[#0070f3] text-white rounded-lg hover:bg-[#005dd4]"
            >
              Закрыть
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-2xl border border-zinc-200 p-6 w-full max-w-md space-y-4">
        <h2 className="text-lg font-semibold text-zinc-900">Новый API-ключ</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Метка</label>
            <input
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="Production key"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">
              Истекает (необязательно)
            </label>
            <input
              type="datetime-local"
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </div>
          {error && (
            <p className="text-sm text-[#d63031] flex items-center gap-1">
              <AlertCircle className="w-4 h-4" /> {error}
            </p>
          )}
          <div className="flex gap-2 justify-end">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm text-zinc-600 border border-zinc-300 rounded-lg hover:border-zinc-400"
            >
              Отмена
            </button>
            <button
              type="submit"
              disabled={loading}
              className="px-4 py-2 text-sm bg-[#0070f3] text-white rounded-lg hover:bg-[#005dd4] disabled:opacity-60 flex items-center gap-2"
            >
              {loading && <Loader2 className="w-4 h-4 animate-spin" />}
              Создать
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function KeysPage({ tenantId }: { tenantId: string }) {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminListKeys(tenantId);
      setKeys(data ?? []);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <main className="min-h-screen bg-zinc-50 py-10 px-4">
      <div className="max-w-3xl mx-auto space-y-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link
              href="/admin/tenants"
              className="text-zinc-400 hover:text-zinc-700 transition-colors"
            >
              <ArrowLeft className="w-5 h-5" />
            </Link>
            <h1 className="text-xl font-bold text-zinc-900">API-ключи</h1>
            <span className="text-xs text-zinc-400 font-mono">{tenantId}</span>
          </div>
          <button
            onClick={() => setShowModal(true)}
            className="flex items-center gap-2 px-4 py-2 bg-[#0070f3] text-white text-sm font-medium rounded-lg hover:bg-[#005dd4]"
          >
            <Plus className="w-4 h-4" />
            Создать ключ
          </button>
        </div>

        <div className="bg-white border border-zinc-200 rounded-2xl px-6">
          {loading ? (
            <div className="py-10 flex justify-center">
              <Loader2 className="w-6 h-6 text-[#0070f3] animate-spin" />
            </div>
          ) : keys.length === 0 ? (
            <p className="py-10 text-center text-zinc-400 text-sm">Нет ключей</p>
          ) : (
            keys.map((k) => (
              <KeyRow
                key={k.id}
                apiKey={k}
                tenantId={tenantId}
                onDeactivated={load}
              />
            ))
          )}
        </div>
      </div>

      {showModal && (
        <NewKeyModal
          tenantId={tenantId}
          onClose={() => setShowModal(false)}
          onCreated={load}
        />
      )}
    </main>
  );
}

export default function AdminKeysPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <AdminGuard>
      <KeysPage tenantId={id} />
    </AdminGuard>
  );
}
