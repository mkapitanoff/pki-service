"use client";

import { useState, FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Loader2, AlertCircle, UserPlus, ShieldCheck } from "lucide-react";
import { register, setAuthToken } from "@/lib/api";

const inputClass =
  "w-full rounded-lg border border-input bg-background px-3 py-2 text-sm focus:border-primary focus:outline-none focus:ring-2 focus:ring-ring/40";

export default function RegisterPage() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const validate = (): string | null => {
    if (!name.trim()) return "Введите имя";
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return "Введите корректный email";
    if (password.length < 8) return "Пароль должен содержать не менее 8 символов";
    if (password !== confirm) return "Пароли не совпадают";
    return null;
  };

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const validationError = validate();
    if (validationError) {
      setError(validationError);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const { token } = await register(email, password, name.trim());
      setAuthToken(token);
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Ошибка регистрации");
      setLoading(false);
    }
  };

  return (
    <main className="min-h-screen bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <span className="inline-flex h-12 w-12 items-center justify-center rounded-xl bg-primary/10 mb-3">
            <ShieldCheck className="h-6 w-6 text-primary" />
          </span>
          <h1 className="text-2xl font-bold text-foreground">PKI Сервис</h1>
          <p className="text-muted-foreground mt-1 text-sm">Подписание PDF-документов ЭЦП РК</p>
        </div>

        <div className="rounded-2xl border border-border bg-card p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-foreground mb-5">Регистрация</h2>

          <form onSubmit={onSubmit} className="space-y-4">
            <div className="space-y-1">
              <label className="text-sm font-medium text-foreground" htmlFor="name">Имя</label>
              <input id="name" type="text" required autoComplete="name" value={name}
                onChange={(e) => setName(e.target.value)} placeholder="Иван Иванов" className={inputClass} />
            </div>

            <div className="space-y-1">
              <label className="text-sm font-medium text-foreground" htmlFor="email">Email</label>
              <input id="email" type="email" required autoComplete="email" value={email}
                onChange={(e) => setEmail(e.target.value)} placeholder="user@example.com" className={inputClass} />
            </div>

            <div className="space-y-1">
              <label className="text-sm font-medium text-foreground" htmlFor="password">Пароль</label>
              <input id="password" type="password" required autoComplete="new-password" value={password}
                onChange={(e) => setPassword(e.target.value)} placeholder="Не менее 8 символов" className={inputClass} />
            </div>

            <div className="space-y-1">
              <label className="text-sm font-medium text-foreground" htmlFor="confirm">Подтверждение пароля</label>
              <input id="confirm" type="password" required autoComplete="new-password" value={confirm}
                onChange={(e) => setConfirm(e.target.value)} placeholder="Повторите пароль" className={inputClass} />
            </div>

            {error && (
              <div className="flex items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{error}</span>
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="flex w-full items-center justify-center gap-2 rounded-xl bg-primary py-2.5 font-semibold text-primary-foreground transition-colors hover:bg-[hsl(var(--primary-hover))] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <UserPlus className="h-4 w-4" />}
              Зарегистрироваться
            </button>
          </form>

          <p className="mt-5 text-center text-sm text-muted-foreground">
            Уже есть аккаунт?{" "}
            <Link href="/login" className="font-medium text-primary hover:underline">
              Войти
            </Link>
          </p>
        </div>
      </div>
    </main>
  );
}
