"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { ArrowLeft, Loader2 } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";
import { adminListUsers, adminUpdateUser, type AdminUser } from "@/lib/api";

const ROLES = ["user", "admin"];

function fmtDate(v: { Time: string; Valid: boolean } | null | undefined): string {
  if (!v?.Valid) return "—";
  return new Date(v.Time).toLocaleDateString("ru-RU");
}

function UserRow({
  user,
  onUpdated,
}: {
  user: AdminUser;
  onUpdated: () => void;
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
    <div className="grid grid-cols-[1fr_auto_auto_auto] gap-4 items-center py-3 border-b border-zinc-100 last:border-0">
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

      <span className="text-xs text-zinc-400">
        {fmtDate(user.last_login_at)}
      </span>
    </div>
  );
}

function UsersPage() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminListUsers();
      setUsers(data ?? []);
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
        <div className="flex items-center gap-3">
          <Link
            href="/admin"
            className="text-zinc-400 hover:text-zinc-700 transition-colors"
          >
            <ArrowLeft className="w-5 h-5" />
          </Link>
          <h1 className="text-xl font-bold text-zinc-900">Пользователи</h1>
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
              <div className="grid grid-cols-[1fr_auto_auto_auto] gap-4 py-2 border-b border-zinc-200 text-xs font-medium text-zinc-400 uppercase">
                <span>Пользователь</span>
                <span>Роль</span>
                <span>Статус</span>
                <span>Посл. вход</span>
              </div>
              {users.map((u) => (
                <UserRow key={u.id} user={u} onUpdated={load} />
              ))}
            </>
          )}
        </div>
      </div>
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
