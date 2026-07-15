"use client";

import Link from "next/link";
import { Building2, Users, Key, ClipboardList } from "lucide-react";
import AdminGuard from "@/components/AdminGuard";

function AdminHome() {
  return (
    <main className="min-h-screen bg-background py-10 px-4">
      <div className="max-w-2xl mx-auto space-y-6">
        <h1 className="text-2xl font-bold text-foreground">Панель администратора</h1>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Link
            href="/admin/tenants"
            className="bg-card border border-border rounded-lg p-6 hover:border-primary/30 transition-colors group"
          >
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-3">
              <Building2 className="w-5 h-5 text-primary" />
            </div>
            <h2 className="text-lg font-semibold text-foreground">
              Тенанты
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              Управление организациями и API-ключами
            </p>
          </Link>

          <Link
            href="/admin/users"
            className="bg-card border border-border rounded-lg p-6 hover:border-primary/30 transition-colors group"
          >
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-3">
              <Users className="w-5 h-5 text-primary" />
            </div>
            <h2 className="text-lg font-semibold text-foreground">
              Пользователи
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              Управление ролями и активацией аккаунтов
            </p>
          </Link>

          <Link
            href="/admin/registry"
            className="bg-card border border-border rounded-lg p-6 hover:border-primary/30 transition-colors group"
          >
            <div className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center mb-3">
              <ClipboardList className="w-5 h-5 text-primary" />
            </div>
            <h2 className="text-lg font-semibold text-foreground">
              Реестр подписаний
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
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
