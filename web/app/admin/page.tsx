"use client";

import Link from "next/link";
import { Building2, Users, ClipboardList, ArrowRight } from "lucide-react";

const CARDS = [
  {
    href: "/admin/registry",
    icon: ClipboardList,
    title: "Реестр подписаний",
    desc: "Отслеживание запросов, скачивание и верификация документов",
  },
  {
    href: "/admin/tenants",
    icon: Building2,
    title: "Тенанты",
    desc: "Управление организациями и API-ключами",
  },
  {
    href: "/admin/users",
    icon: Users,
    title: "Пользователи",
    desc: "Управление ролями и активацией аккаунтов",
  },
];

export default function AdminHome() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-foreground">Панель администратора</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Управление подписаниями, организациями и доступом
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {CARDS.map((c) => {
          const Icon = c.icon;
          return (
            <Link
              key={c.href}
              href={c.href}
              className="group rounded-xl border border-border bg-card p-5 transition-colors hover:border-primary/40"
            >
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10 mb-3">
                <Icon className="h-5 w-5 text-primary" />
              </div>
              <h2 className="text-base font-semibold text-foreground flex items-center gap-1">
                {c.title}
                <ArrowRight className="h-4 w-4 text-muted-foreground opacity-0 -translate-x-1 transition-all group-hover:opacity-100 group-hover:translate-x-0" />
              </h2>
              <p className="text-sm text-muted-foreground mt-1">{c.desc}</p>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
