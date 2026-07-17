import { useCallback, useEffect, useRef, useState } from "react";
import type { SupabaseClient } from "@supabase/supabase-js";

import type { APIClient } from "../../lib/api";
import { HouseholdShell } from "../households/HouseholdShell";
import { AuthForm } from "./AuthForm";
import { RecoveryForm } from "./RecoveryForm";
import { useAuthLifecycle } from "./useAuthLifecycle";

export function AuthHouseholdShell({ api, supabase }: { api: APIClient; supabase: SupabaseClient }) {
  const { state, dispatch } = useAuthLifecycle(supabase);
  const [offline, setOffline] = useState(() => !navigator.onLine);
  const expiringSession = useRef(false);

  useEffect(() => {
    const online = () => setOffline(false);
    const offlineEvent = () => setOffline(true);
    window.addEventListener("online", online);
    window.addEventListener("offline", offlineEvent);
    return () => {
      window.removeEventListener("online", online);
      window.removeEventListener("offline", offlineEvent);
    };
  }, []);

  const expireSession = useCallback(() => {
    if (expiringSession.current) return;
    expiringSession.current = true;
    dispatch({ type: "sessionExpired" });
    void supabase.auth.signOut({ scope: "local" }).then(({ error }) => {
      if (error) {
        dispatch({
          type: "notice",
          message: "API отклонил сессию, но локальное состояние не удалось очистить. Обновите страницу перед новым входом.",
        });
      }
    }).catch(() => {
      dispatch({
        type: "notice",
        message: "API отклонил сессию, но локальное состояние не удалось очистить. Обновите страницу перед новым входом.",
      });
    }).finally(() => {
      expiringSession.current = false;
    });
  }, [dispatch, supabase]);

  if (state.phase === "loading") {
    return <main className="page-shell page-shell--centered"><div className="loading-card" role="status" aria-live="polite"><span className="loading-orbit" aria-hidden="true" /><p>Проверяем защищённую сессию…</p></div></main>;
  }
  if (state.phase === "recovery") {
    return <main className="page-shell page-shell--centered"><RecoveryForm supabase={supabase} offline={offline} /></main>;
  }
  if (state.phase === "signedOut" || !state.user) {
    return <main className="page-shell auth-page"><AuthForm notice={state.notice} offline={offline} supabase={supabase} /><aside className="auth-aside" aria-label="О новой версии"><div><p className="eyebrow">Приватность по умолчанию</p><h2>Один вход.<br />Отдельные пространства.</h2></div><p>Браузер работает только с Supabase Auth. Все прикладные данные проходят через проверяющий JWT Go API.</p><span>React · Go · PostgreSQL</span></aside></main>;
  }

  return (
    <HouseholdShell
      api={api}
      authUser={state.user}
      sessionVersion={state.sessionVersion}
      offline={offline}
      onSessionExpired={expireSession}
      onLogout={async () => {
        const { error } = await supabase.auth.signOut();
        if (error) throw error;
      }}
    />
  );
}
