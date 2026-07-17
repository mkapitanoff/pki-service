"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  LayoutDashboard,
  ClipboardList,
  Building2,
  Users,
  ShieldCheck,
  LogOut,
  Menu,
  X,
  PanelLeft,
  User as UserIcon,
} from "lucide-react";
import { me, logout, type User } from "@/lib/api";
import { cn } from "@/lib/utils";
import { ThemeToggle } from "@/components/ThemeToggle";

type NavItem = {
  href: string;
  label: string;
  icon: typeof LayoutDashboard;
  exact?: boolean;
};

const NAV_GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Обзор",
    items: [{ href: "/admin", label: "Дашборд", icon: LayoutDashboard, exact: true }],
  },
  {
    label: "Управление",
    items: [
      { href: "/admin/registry", label: "Реестр подписаний", icon: ClipboardList },
      { href: "/admin/tenants", label: "Тенанты", icon: Building2 },
      { href: "/admin/users", label: "Пользователи", icon: Users },
    ],
  },
];
const ALL_ITEMS = NAV_GROUPS.flatMap((g) => g.items);

function isActive(pathname: string, href: string, exact?: boolean) {
  if (exact) return pathname === href;
  return pathname === href || pathname.startsWith(href + "/");
}

function pageTitle(pathname: string): string {
  if (pathname.includes("/keys")) return "API-ключи";
  const match = ALL_ITEMS.find((i) => isActive(pathname, i.href, i.exact));
  return match?.label ?? "Администрирование";
}

export default function AdminShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [open, setOpen] = useState(false); // mobile drawer
  const [collapsed, setCollapsed] = useState(false); // desktop rail

  useEffect(() => {
    me().then(setUser).catch(() => setUser(null));
    setCollapsed(localStorage.getItem("admin.sidebar.collapsed") === "1");
  }, []);

  useEffect(() => setOpen(false), [pathname]);

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      const next = !c;
      localStorage.setItem("admin.sidebar.collapsed", next ? "1" : "0");
      return next;
    });
  };

  const handleLogout = async () => {
    await logout();
    router.push("/login");
  };

  // ── Sidebar building blocks ─────────────────────────────────────────────
  const brand = (mini: boolean) => (
    <Link
      href="/admin"
      className={cn(
        "flex h-16 shrink-0 items-center gap-2 border-b border-sidebar-border",
        mini ? "justify-center px-0" : "px-5",
      )}
    >
      <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10">
        <ShieldCheck className="h-5 w-5 text-primary" />
      </span>
      {!mini && (
        <span className="flex flex-col leading-tight">
          <span className="font-bold text-sidebar-foreground tracking-tight">PKI · Chandra</span>
          <span className="text-[11px] text-muted-foreground">Подписание ЭЦП РК</span>
        </span>
      )}
    </Link>
  );

  const nav = (mini: boolean) => (
    <nav className="flex-1 space-y-4 overflow-y-auto px-3 py-4">
      {NAV_GROUPS.map((group) => (
        <div key={group.label} className="space-y-1">
          {!mini && (
            <p className="px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70">
              {group.label}
            </p>
          )}
          {group.items.map((item) => {
            const active = isActive(pathname, item.href, item.exact);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                title={mini ? item.label : undefined}
                className={cn(
                  "flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                  mini && "justify-center px-0",
                  active
                    ? "bg-sidebar-accent text-sidebar-primary"
                    : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground",
                )}
              >
                <Icon className="h-4 w-4 shrink-0" />
                {!mini && item.label}
              </Link>
            );
          })}
        </div>
      ))}
    </nav>
  );

  return (
    <div className="min-h-screen bg-background">
      {/* Desktop sidebar */}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-30 hidden flex-col border-r border-sidebar-border bg-sidebar transition-[width] duration-200 lg:flex",
          collapsed ? "w-16" : "w-64",
        )}
      >
        {brand(collapsed)}
        {nav(collapsed)}
      </aside>

      {/* Content column */}
      <div className={cn("transition-[padding] duration-200", collapsed ? "lg:pl-16" : "lg:pl-64")}>
        {/* Top bar */}
        <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-3 border-b border-border bg-card px-4">
          <div className="flex min-w-0 items-center gap-2">
            {/* Mobile: open drawer */}
            <button
              onClick={() => setOpen(true)}
              className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-muted/60 hover:text-foreground lg:hidden"
              aria-label="Открыть меню"
            >
              <Menu className="h-5 w-5" />
            </button>
            {/* Desktop: collapse rail */}
            <button
              onClick={toggleCollapsed}
              className="hidden h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-muted/60 hover:text-foreground lg:flex"
              aria-label="Свернуть меню"
            >
              <PanelLeft className="h-5 w-5" />
            </button>
            <h1 className="truncate text-sm font-semibold text-foreground sm:text-base">
              {pageTitle(pathname)}
            </h1>
          </div>

          <div className="flex items-center gap-1.5">
            <ThemeToggle />
            {user && (
              <div className="ml-1 flex items-center gap-2 rounded-full border border-border bg-background py-1 pl-1 pr-3">
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-primary/10 text-primary">
                  <UserIcon className="h-4 w-4" />
                </span>
                <span className="hidden min-w-0 flex-col leading-tight sm:flex">
                  <span className="truncate text-xs font-medium text-foreground">{user.name}</span>
                  <span className="truncate text-[11px] text-muted-foreground">{user.email}</span>
                </span>
              </div>
            )}
            <button
              onClick={handleLogout}
              className="flex h-9 w-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
              aria-label="Выйти"
              title="Выйти"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        </header>

        <main>
          <div className="mx-auto w-full max-w-6xl p-6 sm:p-8">{children}</div>
        </main>
      </div>

      {/* Mobile drawer */}
      {open && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setOpen(false)} />
          <aside className="absolute inset-y-0 left-0 flex w-64 flex-col bg-sidebar shadow-lg">
            <div className="flex items-center justify-between border-b border-sidebar-border pr-3">
              {brand(false)}
              <button
                onClick={() => setOpen(false)}
                className="text-muted-foreground hover:text-foreground"
                aria-label="Закрыть меню"
              >
                <X className="h-5 w-5" />
              </button>
            </div>
            {nav(false)}
          </aside>
        </div>
      )}
    </div>
  );
}
