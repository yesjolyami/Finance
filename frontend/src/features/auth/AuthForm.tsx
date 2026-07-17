import { useEffect, useState, type FormEvent } from "react";
import type { SupabaseClient } from "@supabase/supabase-js";

type AuthMode = "signIn" | "signUp" | "reset";

interface AuthFormProps {
  notice: string | null;
  offline: boolean;
  supabase: SupabaseClient;
}

const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function safeAuthError(offline: boolean): string {
  return offline ? "Нет соединения с интернетом." : "Не удалось выполнить запрос. Проверьте данные и попробуйте снова.";
}

export function AuthForm({ notice, offline, supabase }: AuthFormProps) {
  const [mode, setMode] = useState<AuthMode>("signIn");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string | null>(notice);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (notice) setMessage(notice);
  }, [notice]);

  const changeMode = (next: AuthMode) => {
    if (pending) return;
    setMode(next);
    setPassword("");
    setConfirmation("");
    setError(null);
    setMessage(null);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
    const normalizedEmail = email.trim().toLowerCase();
    setError(null);
    setMessage(null);
    if (!emailPattern.test(normalizedEmail)) {
      setError("Введите корректный email.");
      return;
    }
    if (mode !== "reset" && password.length < 8) {
      setError("Пароль должен содержать минимум 8 символов.");
      return;
    }
    if (mode === "signUp" && password !== confirmation) {
      setError("Пароли не совпадают.");
      return;
    }

    setPending(true);
    try {
      if (mode === "signIn") {
        const { error: authError } = await supabase.auth.signInWithPassword({ email: normalizedEmail, password });
        if (authError) throw authError;
      } else if (mode === "signUp") {
        const { error: authError } = await supabase.auth.signUp({ email: normalizedEmail, password });
        if (authError) throw authError;
        setMessage("Регистрация создана. Если подтверждение email включено, проверьте почту.");
      } else {
        const { error: authError } = await supabase.auth.resetPasswordForEmail(normalizedEmail, {
          redirectTo: window.location.origin,
        });
        if (authError) throw authError;
        setMessage("Если аккаунт существует, инструкция по восстановлению будет отправлена.");
      }
      setPassword("");
      setConfirmation("");
    } catch {
      setError(safeAuthError(offline));
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="auth-card" aria-labelledby="auth-title">
      <div className="auth-card__intro">
        <p className="eyebrow">Монетка · защищённый вход</p>
        <h1 id="auth-title">
          {mode === "signIn" && "Ваши финансы начинаются с личного пространства"}
          {mode === "signUp" && "Создайте защищённый профиль"}
          {mode === "reset" && "Восстановите доступ"}
        </h1>
        <p className="lead">Добро пожаловать в «Монетку». Ваши данные доступны только после защищённой авторизации.</p>
      </div>

      <form className="auth-form" onSubmit={(event) => void submit(event)} noValidate>
        <label htmlFor="auth-email">Email</label>
        <input
          id="auth-email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          disabled={pending}
          required
        />
        {mode !== "reset" && (
          <>
            <label htmlFor="auth-password">Пароль</label>
            <input
              id="auth-password"
              type="password"
              autoComplete={mode === "signIn" ? "current-password" : "new-password"}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              disabled={pending}
              minLength={8}
              required
            />
          </>
        )}
        {mode === "signUp" && (
          <>
            <label htmlFor="auth-confirmation">Повторите пароль</label>
            <input
              id="auth-confirmation"
              type="password"
              autoComplete="new-password"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              disabled={pending}
              minLength={8}
              required
            />
          </>
        )}

        <div className="form-message" aria-live="polite">
          {(error || message || offline) && <p data-kind={error || offline ? "error" : "success"}>{error ?? message ?? "Вы офлайн."}</p>}
        </div>
        <button className="primary-button" type="submit" disabled={pending || offline}>
          {pending ? "Подождите…" : mode === "signIn" ? "Войти" : mode === "signUp" ? "Зарегистрироваться" : "Отправить инструкцию"}
        </button>
      </form>

      <nav className="auth-switcher" aria-label="Действия с аккаунтом">
        {mode !== "signIn" && <button type="button" disabled={pending} onClick={() => changeMode("signIn")}>Вернуться ко входу</button>}
        {mode === "signIn" && <button type="button" disabled={pending} onClick={() => changeMode("signUp")}>Создать аккаунт</button>}
        {mode === "signIn" && <button type="button" disabled={pending} onClick={() => changeMode("reset")}>Забыли пароль?</button>}
      </nav>
    </section>
  );
}
