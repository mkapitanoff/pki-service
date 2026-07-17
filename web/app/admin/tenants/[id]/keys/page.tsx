"use client";

import { use, useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { Plus, Loader2, AlertCircle, Trash2, Copy, CheckCheck, CheckCircle2, Circle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
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
    <div className="flex items-center justify-between py-3 border-b border-border/50 last:border-0">
      <div>
        <p className="font-medium text-foreground">{apiKey.label}</p>
        <p className="text-xs text-muted-foreground font-mono mt-0.5">{apiKey.id}</p>
        <p className="text-xs text-muted-foreground mt-0.5">
          Истекает: {fmtDate(apiKey.expires_at)}
        </p>
      </div>
      <div className="flex items-center gap-3">
        <Badge tone={apiKey.is_active ? "success" : "muted"}>
          {apiKey.is_active ? <CheckCircle2 className="h-3 w-3" /> : <Circle className="h-3 w-3" />}
          {apiKey.is_active ? "Активен" : "Неактивен"}
        </Badge>
        {apiKey.is_active && (
          <button
            onClick={handleDeactivate}
            disabled={loading}
            className="text-muted-foreground hover:text-destructive transition-colors"
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
        <div className="bg-card rounded-lg border border-border p-6 w-full max-w-md space-y-4">
          <h2 className="text-lg font-semibold text-foreground">Ключ создан</h2>
          <p className="text-sm text-muted-foreground">
            Сохраните ключ — он отображается только один раз.
          </p>
          <div className="bg-muted border border-border rounded-lg p-3 flex items-center gap-2">
            <code className="text-xs font-mono break-all flex-1 text-foreground">
              {createdKey}
            </code>
            <button onClick={handleCopy} className="shrink-0 text-muted-foreground hover:text-foreground">
              {copied ? (
                <CheckCheck className="w-4 h-4 text-success" />
              ) : (
                <Copy className="w-4 h-4" />
              )}
            </button>
          </div>
          <div className="flex justify-end">
            <button
              onClick={onClose}
              className="px-4 py-2 text-sm bg-primary text-primary-foreground rounded-lg hover:opacity-90"
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
      <div className="bg-card rounded-lg border border-border p-6 w-full max-w-md space-y-4">
        <h2 className="text-lg font-semibold text-foreground">Новый API-ключ</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-muted-foreground mb-1">Метка</label>
            <input
              className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="Production key"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-muted-foreground mb-1">
              Истекает (необязательно)
            </label>
            <input
              type="datetime-local"
              className="w-full border border-input rounded-lg px-3 py-2 text-sm bg-background focus:outline-none focus:border-primary"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </div>
          {error && (
            <p className="text-sm text-destructive flex items-center gap-1">
              <AlertCircle className="w-4 h-4" /> {error}
            </p>
          )}
          <div className="flex gap-2 justify-end">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm text-muted-foreground border border-border rounded-lg hover:bg-muted/50"
            >
              Отмена
            </button>
            <button
              type="submit"
              disabled={loading}
              className="px-4 py-2 text-sm bg-primary text-primary-foreground rounded-lg hover:opacity-90 disabled:opacity-60 flex items-center gap-2"
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
    <div>
      <div className="space-y-6">
        <div className="flex items-start justify-between gap-3">
          <div>
            <nav className="mb-1 flex items-center gap-1.5 text-sm text-muted-foreground">
              <Link href="/admin/tenants" className="hover:text-foreground">Тенанты</Link>
              <span>/</span>
              <span className="text-foreground">API-ключи</span>
            </nav>
            <h1 className="text-2xl font-bold text-foreground">API-ключи</h1>
            <p className="mt-1 font-mono text-xs text-muted-foreground">{tenantId}</p>
          </div>
          <button
            onClick={() => setShowModal(true)}
            className="flex shrink-0 items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            <Plus className="h-4 w-4" />
            <span className="hidden sm:inline">Создать ключ</span>
          </button>
        </div>

        <div className="bg-card border border-border rounded-lg px-6">
          {loading ? (
            <div className="py-10 flex justify-center">
              <Loader2 className="w-6 h-6 text-primary animate-spin" />
            </div>
          ) : keys.length === 0 ? (
            <p className="py-10 text-center text-muted-foreground text-sm">Нет ключей</p>
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
    </div>
  );
}

export default function AdminKeysPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return <KeysPage tenantId={id} />;
}
