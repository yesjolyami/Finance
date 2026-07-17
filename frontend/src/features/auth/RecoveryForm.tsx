import { useState, type FormEvent } from "react";
import type { SupabaseClient } from "@supabase/supabase-js";

export function RecoveryForm({ supabase, offline }: { supabase: SupabaseClient; offline: boolean }) {
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
    if (password.length < 8) {
      setMessage("Пароль должен содержать минимум 8 символов.");
      return;
    }
    if (password !== confirmation) {
      setMessage("Пароли не совпадают.");
      return;
    }
    setPending(true);
    setMessage(null);
    try {
      const { error } = await supabase.auth.updateUser({ password });
      if (error) throw error;
      const { error: signOutError } = await supabase.auth.signOut({ scope: "local" });
      if (signOutError) throw signOutError;
      setPassword("");
      setConfirmation("");
    } catch {
      setMessage(offline ? "Нет соединения с интернетом." : "Не удалось обновить пароль. Запросите новую ссылку.");
    } finally {
      setPending(false);
    }
  };

  const returnToSignIn = async () => {
    if (pending) return;
    setPending(true);
    setMessage(null);
    try {
      const { error } = await supabase.auth.signOut({ scope: "local" });
      if (error) throw error;
      setPassword("");
      setConfirmation("");
    } catch {
      setMessage("Не удалось завершить локальную recovery-сессию. Обновите страницу и запросите новую ссылку.");
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="auth-card auth-card--compact" aria-labelledby="recovery-title">
      <p className="eyebrow">Монетка · восстановление доступа</p>
      <h1 id="recovery-title">Установите новый пароль</h1>
      <form className="auth-form" onSubmit={(event) => void submit(event)}>
        <label htmlFor="recovery-password">Новый пароль</label>
        <input id="recovery-password" type="password" autoComplete="new-password" minLength={8} value={password} onChange={(event) => setPassword(event.target.value)} disabled={pending} required />
        <label htmlFor="recovery-confirmation">Повторите новый пароль</label>
        <input id="recovery-confirmation" type="password" autoComplete="new-password" minLength={8} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} disabled={pending} required />
        <div className="form-message" aria-live="polite">{message && <p data-kind="error">{message}</p>}</div>
        <button className="primary-button" type="submit" disabled={pending || offline}>{pending ? "Сохраняем…" : "Сохранить пароль"}</button>
      </form>
      <button className="text-button" type="button" onClick={() => void returnToSignIn()} disabled={pending}>
        Вернуться ко входу или запросить новую ссылку
      </button>
    </section>
  );
}
