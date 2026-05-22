"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { ArrowLeft, Loader2, Plus, Trash2, AlertCircle } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";
import {
  adminListUsers,
  adminUpdateUser,
  adminCreateUser,
  adminDeleteUser,
  adminListTenants,
  type AdminUser,
  type Tenant,
} from "@/lib/api";

const ROLES = ["user", "admin"];

function fmtDate(v: { Time: string; Valid: boolean } | null | undefined): string {
  if (!v?.Valid) return "—";
  return new Date(v.Time).toLocaleDateString("ru-RU");
}

// ---- Модалка подтверждения удаления ----

function DeleteConfirmModal({
  user,
  onClose,
  onDeleted,
}: {
  user: AdminUser;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleDelete = async () => {
    setLoading(true);
    setError(null);
    try {
      await adminDeleteUser(user.id);
      onDeleted();
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
        <h2 className="text-lg font-semibold text-zinc-900">Удалить пользователя?</h2>
        <p className="text-sm text-zinc-600">
          Удалить пользователя <span className="font-medium text-zinc-900">{user.email}</span>?{" "}
          Это действие нельзя отменить.
        </p>
        {error && (
          <p className="text-sm text-[#d63031] flex items-center gap-1">
            <AlertCircle className="w-4 h-4" /> {error}
          </p>
        )}
        <div className="flex gap-2 justify-end">
          <button
            type="button"
            onClick={onClose}
            disabled={loading}
            className="px-4 py-2 text-sm text-zinc-600 border border-zinc-300 rounded-lg hover:border-zinc-400 disabled:opacity-60"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={handleDelete}
            disabled={loading}
            className="px-4 py-2 text-sm bg-[#d63031] text-white rounded-lg hover:bg-[#b02020] disabled:opacity-60 flex items-center gap-2"
          >
            {loading && <Loader2 className="w-4 h-4 animate-spin" />}
            Удалить
          </button>
        </div>
      </div>
    </div>
  );
}

// ---- Модалка создания пользователя ----

function CreateUserModal({
  tenants,
  onClose,
  onCreated,
}: {
  tenants: Tenant[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("user");
  const [tenantId, setTenantId] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password || !name) return;
    setLoading(true);
    setError(null);
    try {
      await adminCreateUser(email, password, name, role, tenantId || undefined);
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
        <h2 className="text-lg font-semibold text-zinc-900">Новый пользователь</h2>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Имя</label>
            <input
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Иван Иванов"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Email</label>
            <input
              type="email"
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="user@example.com"
              required
            />
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Пароль</label>
            <input
              type="password"
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Минимум 8 символов"
              required
              minLength={8}
            />
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">Роль</label>
            <select
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={role}
              onChange={(e) => setRole(e.target.value)}
            >
              <option value="user">user</option>
              <option value="admin">admin</option>
            </select>
          </div>
          <div>
            <label className="block text-sm text-zinc-600 mb-1">
              Тенант (необязательно — создастся автоматически)
            </label>
            <select
              className="w-full border border-zinc-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-[#0070f3]"
              value={tenantId}
              onChange={(e) => setTenantId(e.target.value)}
            >
              <option value="">Создать новый персональный тенант</option>
              {tenants.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          </div>
          {error && (
            <p className="text-sm text-[#d63031] flex items-center gap-1">
              <AlertCircle className="w-4 h-4" /> {error}
            </p>
          )}
          <div className="flex gap-2 justify-end pt-1">
            <button
              type="button"
              onClick={onClose}
              disabled={loading}
              className="px-4 py-2 text-sm text-zinc-600 border border-zinc-300 rounded-lg hover:border-zinc-400 disabled:opacity-60"
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

// ---- Строка пользователя ----

function UserRow({
  user,
  onUpdated,
  onDeleteRequest,
}: {
  user: AdminUser;
  onUpdated: () => void;
  onDeleteRequest: (u: AdminUser) => void;
}) {
  const [saving, setSaving] = useState(false);
  const isActive = user.is_active?.Bool ?? true;

  const handleRoleChange = async (role: string) => {
    setSaving(true);
    try {
      await adminUpdateUser(user.id, role, isActive);
      onUpdated();
    } finally {
      setSaving(false);
    }
  };

  const handleToggleActive = async () => {
    setSaving(true);
    try {
      await adminUpdateUser(user.id, user.role, !isActive);
      onUpdated();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="grid grid-cols-[1fr_auto_auto_auto_auto] gap-3 items-center py-3 border-b border-zinc-100 last:border-0">
      <div className="min-w-0">
        <p className="font-medium text-zinc-900 truncate">{user.name}</p>
        <p className="text-xs text-zinc-400 truncate">{user.email}</p>
        <p className="text-xs text-zinc-300 font-mono mt-0.5">
          Создан: {fmtDate(user.created_at)}
        </p>
      </div>

      <select
        value={user.role}
        onChange={(e) => handleRoleChange(e.target.value)}
        disabled={saving}
        className="text-xs border border-zinc-300 rounded-lg px-2 py-1 focus:outline-none focus:border-[#0070f3] disabled:opacity-60"
      >
        {ROLES.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </select>

      <button
        onClick={handleToggleActive}
        disabled={saving}
        className={`text-xs font-medium px-2 py-1 rounded-full border transition-colors ${
          isActive
            ? "bg-green-50 text-green-700 border-green-200 hover:bg-red-50 hover:text-red-600 hover:border-red-200"
            : "bg-zinc-100 text-zinc-400 border-zinc-200 hover:bg-green-50 hover:text-green-700 hover:border-green-200"
        } disabled:opacity-60`}
      >
        {saving ? (
          <Loader2 className="w-3 h-3 animate-spin" />
        ) : isActive ? (
          "Активен"
        ) : (
          "Неактивен"
        )}
      </button>

      <span className="text-xs text-zinc-400 shrink-0">
        {fmtDate(user.last_login_at)}
      </span>

      <button
        onClick={() => onDeleteRequest(user)}
        className="text-zinc-300 hover:text-[#d63031] transition-colors"
        title="Удалить пользователя"
      >
        <Trash2 className="w-4 h-4" />
      </button>
    </div>
  );
}

// ---- Основная страница ----

function UsersPage() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<AdminUser | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [u, t] = await Promise.all([adminListUsers(), adminListTenants()]);
      setUsers(u ?? []);
      setTenants(t ?? []);
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
            <h1 className="text-xl font-bold text-zinc-900">Пользователи</h1>
          </div>
          <button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-2 px-4 py-2 bg-[#0070f3] text-white text-sm font-medium rounded-lg hover:bg-[#005dd4]"
          >
            <Plus className="w-4 h-4" />
            Добавить
          </button>
        </div>

        <div className="bg-white border border-zinc-200 rounded-2xl px-6">
          {loading ? (
            <div className="py-10 flex justify-center">
              <Loader2 className="w-6 h-6 text-[#0070f3] animate-spin" />
            </div>
          ) : users.length === 0 ? (
            <p className="py-10 text-center text-zinc-400 text-sm">Нет пользователей</p>
          ) : (
            <>
              <div className="grid grid-cols-[1fr_auto_auto_auto_auto] gap-3 py-2 border-b border-zinc-200 text-xs font-medium text-zinc-400 uppercase">
                <span>Пользователь</span>
                <span>Роль</span>
                <span>Статус</span>
                <span>Посл. вход</span>
                <span></span>
              </div>
              {users.map((u) => (
                <UserRow
                  key={u.id}
                  user={u}
                  onUpdated={load}
                  onDeleteRequest={setDeleteTarget}
                />
              ))}
            </>
          )}
        </div>
      </div>

      {showCreate && (
        <CreateUserModal
          tenants={tenants}
          onClose={() => setShowCreate(false)}
          onCreated={load}
        />
      )}

      {deleteTarget && (
        <DeleteConfirmModal
          user={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={load}
        />
      )}
    </main>
  );
}

export default function AdminUsersPage() {
  return (
    <AdminGuard>
      <UsersPage />
    </AdminGuard>
  );
}
