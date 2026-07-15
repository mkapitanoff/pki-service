"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { LogOut, LogIn, ShieldCheck } from "lucide-react";
import { me, logout, getAuthToken, clearAuthToken, type User } from "@/lib/api";

export default function Header() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const loadUser = async () => {
      const token = getAuthToken();
      if (!token) {
        setUser(null);
        setReady(true);
        return;
      }
      try {
        const u = await me();
        setUser(u);
      } catch {
        clearAuthToken();
        setUser(null);
      }
      setReady(true);
    };

    loadUser();

    // Обновлять при изменении токена в localStorage (cross-tab и после логина)
    const handleStorage = () => loadUser();
    window.addEventListener("storage", handleStorage);

    // Обновлять при возврате на вкладку
    window.addEventListener("focus", loadUser);

    return () => {
      window.removeEventListener("storage", handleStorage);
      window.removeEventListener("focus", loadUser);
    };
  }, []);

  const handleLogout = async () => {
    await logout();
    setUser(null);
    router.push("/login");
  };

  return (
    <header className="bg-card border-b border-border px-4 py-3 flex items-center justify-between shrink-0">
      <Link href="/" className="font-bold text-foreground text-base tracking-tight">
        PKI Сервис
      </Link>

      <div className="flex items-center gap-3">
        {ready && (
          user ? (
            <>
              <span className="text-sm text-muted-foreground hidden sm:block">
                {user.name}
              </span>
              <span className="text-xs text-muted-foreground font-medium bg-muted px-2 py-0.5 rounded-full hidden sm:block">
                {user.role}
              </span>
              {user.role === "admin" && (
                <Link
                  href="/admin"
                  className="flex items-center gap-1.5 text-sm text-primary hover:underline"
                >
                  <ShieldCheck className="w-4 h-4" />
                  <span className="hidden sm:inline">Админ</span>
                </Link>
              )}
              <button
                onClick={handleLogout}
                className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-destructive transition-colors"
              >
                <LogOut className="w-4 h-4" />
                <span className="hidden sm:inline">Выйти</span>
              </button>
            </>
          ) : (
            <Link
              href="/login"
              className="flex items-center gap-1.5 text-sm text-primary hover:underline"
            >
              <LogIn className="w-4 h-4" />
              Войти
            </Link>
          )
        )}
      </div>
    </header>
  );
}
