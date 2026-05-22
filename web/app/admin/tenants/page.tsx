"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { ArrowLeft, Plus, Key, Loader2, AlertCircle } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";
import { adminListTenants, adminCreateTenant, type Tenant } from "@/lib/api";

function TenantRow({ tenant }: { tenant: Tenant }) {
  return (
    <div className="flex items-center justify-between py-3 border-b border-zinc-100 last:border-0">
      <div>
        <p className="font-medium text-zinc-900">{tenant.name}</p>
        <p className="text-xs text-zinc-400 font-mono mt-0.5">{tenant.id}</p>
      </div>
      <div className="flex items-center gap-4">
        <span className="text-xs text-zinc-500">
          {tenant.type === "individual" ? "Физ. лицо" : "Юр. лицо"}
        </span>
        <span className="flex items-center gap-1 text-xs text-zinc-500">
          <Key className="w-3.5 h-3.5" />
          {String(tenant.api_keys_count)}
        </span>
        <span
          className={`text-xs font-medium px-2 py-0.5 rounded-full ${
            tenant.is_active
              ? "bg-green-50 text-green-700"
              : "bg-zinc-100 text-zinc-400"
          }`}
        >
          {tenant.is_active ? "Активен" : "Неактивен"}
        </span>
        <Link
          href={`/admin/tenants/${tenant.id}/keys`}
          className="text-xs text-[#0070f3] hover:underline"
        >
          API-ключи
        </Link>
      </div>
    </div>
  );
}

function CreateTenantModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState("");
  const [type, setType] = useState("legal_entity");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setLoading(true);
    setError(null);
    try {
      await adminCreateTenant(name.trim(), type);
      onCreated();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Ошибка");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-2xl border border-zinc-200 p-6 w-full max-w-md space-y-4">
        <h2 className="text-lg font-semibold text-zinc-900">Новый тенант</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Название</label>
            <input
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="ТОО Пример"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Тип</label>
            <select
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={type}
              onChange={(e) => setType(e.target.value)}
            >
              <option value="legal_entity">Юридическое лицо</option>
              <option value="individual">Физическое лицо</option>
            </select>
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

function TenantsPage() {
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminListTenants();
      setTenants(data ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <main className="min-h-screen bg-zinc-50 py-10 px-4">
      <div className="max-w-3xl mx-auto space-y-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link
              href="/admin"
              className="text-zinc-400 hover:text-zinc-700 transition-colors"
            >
              <ArrowLeft className="w-5 h-5" />
            </Link>
            <h1 className="text-xl font-bold text-zinc-900">Тенанты</h1>
          </div>
          <button
            onClick={() => setShowModal(true)}
            className="flex items-center gap-2 px-4 py-2 bg-[#0070f3] text-white text-sm font-medium rounded-lg hover:bg-[#005dd4]"
          >
            <Plus className="w-4 h-4" />
            Создать
          </button>
        </div>

        <div className="bg-white border border-zinc-200 rounded-2xl px-6">
          {loading ? (
            <div className="py-10 flex justify-center">
              <Loader2 className="w-6 h-6 text-[#0070f3] animate-spin" />
            </div>
          ) : tenants.length === 0 ? (
            <p className="py-10 text-center text-zinc-400 text-sm">Нет тенантов</p>
          ) : (
            tenants.map((t) => <TenantRow key={t.id} tenant={t} />)
          )}
        </div>
      </div>

      {showModal && (
        <CreateTenantModal
          onClose={() => setShowModal(false)}
          onCreated={load}
        />
      )}
    </main>
  );
}

export default function AdminTenantsPage() {
  return (
    <AdminGuard>
      <TenantsPage />
    </AdminGuard>
  );
}
