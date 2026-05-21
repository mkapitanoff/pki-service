"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { LogOut, LogIn } from "lucide-react";
import { me, logout, getAuthToken, type User } from "@/lib/api";

export default function Header() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const token = getAuthToken();
    if (!token) {
      setReady(true);
      return;
    }
    me()
      .then((u) => setUser(u))
      .catch(() => setUser(null))
      .finally(() => setReady(true));
  }, []);

  const handleLogout = async () => {
    await logout();
    setUser(null);
    router.push("/login");
  };

  return (
    <header className="bg-white border-b border-zinc-200 px-4 py-3 flex items-center justify-between shrink-0">
      <Link href="/" className="font-bold text-zinc-900 text-base tracking-tight">
        PKI Сервис
      </Link>

      <div className="flex items-center gap-3">
        {ready && (
          user ? (
            <>
              <span className="text-sm text-zinc-600 hidden sm:block">
                {user.name}
              </span>
              <span className="text-xs text-zinc-400 font-medium bg-zinc-100 px-2 py-0.5 rounded-full hidden sm:block">
                {user.role}
              </span>
              <button
                onClick={handleLogout}
                className="flex items-center gap-1.5 text-sm text-zinc-500 hover:text-[#d63031] transition-colors"
              >
                <LogOut className="w-4 h-4" />
                <span className="hidden sm:inline">Выйти</span>
              </button>
            </>
          ) : (
            <Link
              href="/login"
              className="flex items-center gap-1.5 text-sm text-[#0070f3] hover:underline"
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
