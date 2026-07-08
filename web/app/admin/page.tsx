"use client";

import Link from "next/link";
import { Building2, Users, Key, ClipboardList } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";

function AdminHome() {
  return (
    <main className="min-h-screen bg-zinc-50 py-10 px-4">
      <div className="max-w-2xl mx-auto space-y-6">
        <h1 className="text-2xl font-bold text-zinc-900">Панель администратора</h1>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Link
            href="/admin/tenants"
            className="bg-white border border-zinc-200 rounded-2xl p-6 hover:border-[#0070f3] transition-colors group"
          >
            <Building2 className="w-8 h-8 text-[#0070f3] mb-3" />
            <h2 className="text-lg font-semibold text-zinc-900 group-hover:text-[#0070f3]">
              Тенанты
            </h2>
            <p className="text-sm text-zinc-500 mt-1">
              Управление организациями и API-ключами
            </p>
          </Link>

          <Link
            href="/admin/users"
            className="bg-white border border-zinc-200 rounded-2xl p-6 hover:border-[#0070f3] transition-colors group"
          >
            <Users className="w-8 h-8 text-[#0070f3] mb-3" />
            <h2 className="text-lg font-semibold text-zinc-900 group-hover:text-[#0070f3]">
              Пользователи
            </h2>
            <p className="text-sm text-zinc-500 mt-1">
              Управление ролями и активацией аккаунтов
            </p>
          </Link>

          <Link
            href="/admin/registry"
            className="bg-white border border-zinc-200 rounded-2xl p-6 hover:border-[#0070f3] transition-colors group"
          >
            <ClipboardList className="w-8 h-8 text-[#0070f3] mb-3" />
            <h2 className="text-lg font-semibold text-zinc-900 group-hover:text-[#0070f3]">
              Реестр подписаний
            </h2>
            <p className="text-sm text-zinc-500 mt-1">
              Отслеживание запросов, скачивание и верификация документов
            </p>
          </Link>
        </div>
      </div>
    </main>
  );
}

export default function AdminPage() {
  return (
    <AdminGuard>
      <AdminHome />
    </AdminGuard>
  );
}
