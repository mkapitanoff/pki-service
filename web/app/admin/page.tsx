"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  ClipboardList,
  Building2,
  Users,
  FileSignature,
  CheckCircle2,
  Clock,
  AlertCircle,
  ArrowRight,
} from "lucide-react";
import { adminListRegistry } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { IconPill, type IconPillTone } from "@/components/ui/icon-pill";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber } from "@/lib/format";

type Stats = { total: number; uploaded: number; processing: number; errors: number };

async function count(status?: string): Promise<number> {
  const r = await adminListRegistry({ status, limit: 1 });
  return r.total ?? 0;
}

const LINKS = [
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
  const [stats, setStats] = useState<Stats | null>(null);
  const [err, setErr] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const [total, uploaded, ff, uf] = await Promise.all([
          count(),
          count("uploaded"),
          count("fetch_failed"),
          count("upload_failed"),
        ]);
        const errors = ff + uf;
        setStats({ total, uploaded, errors, processing: Math.max(0, total - uploaded - errors) });
      } catch {
        setErr(true);
      }
    })();
  }, []);

  const kpis: {
    label: string;
    value?: number;
    tone: IconPillTone;
    icon: typeof FileSignature;
    sub: string;
  }[] = [
    { label: "Всего подписаний", value: stats?.total, tone: "primary", icon: FileSignature, sub: "документов в реестре" },
    { label: "Готово", value: stats?.uploaded, tone: "success", icon: CheckCircle2, sub: "выгружено в S3" },
    { label: "В обработке", value: stats?.processing, tone: "warning", icon: Clock, sub: "подписание / выгрузка" },
    { label: "Ошибки", value: stats?.errors, tone: "destructive", icon: AlertCircle, sub: "требуют внимания" },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-foreground">Панель администратора</h1>
        <p className="text-sm text-muted-foreground mt-1">Обзор подписаний и управление доступом</p>
      </div>

      {/* KPI */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {kpis.map((k) => {
          const Icon = k.icon;
          return (
            <Card key={k.label} className="p-5">
              <IconPill tone={k.tone}>
                <Icon className="h-5 w-5" />
              </IconPill>
              <p className="mt-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                {k.label}
              </p>
              {k.value === undefined ? (
                <Skeleton className="mt-1 h-9 w-16" />
              ) : (
                <p className="text-3xl font-bold text-foreground tabular-nums">{formatNumber(k.value)}</p>
              )}
              <p className="mt-1 text-xs text-muted-foreground">{k.sub}</p>
            </Card>
          );
        })}
      </div>
      {err && (
        <p className="text-sm text-destructive">Не удалось загрузить статистику реестра.</p>
      )}

      {/* Quick links */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {LINKS.map((c) => {
          const Icon = c.icon;
          return (
            <Link
              key={c.href}
              href={c.href}
              className="group rounded-xl border border-border bg-card p-5 transition-colors hover:border-primary/40"
            >
              <IconPill tone="primary" className="mb-3">
                <Icon className="h-5 w-5" />
              </IconPill>
              <h2 className="flex items-center gap-1 text-base font-semibold text-foreground">
                {c.title}
                <ArrowRight className="h-4 w-4 text-muted-foreground opacity-0 -translate-x-1 transition-all group-hover:translate-x-0 group-hover:opacity-100" />
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">{c.desc}</p>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
