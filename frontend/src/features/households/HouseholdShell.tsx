import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { APIClient, APIError, type Household, type UserProfile } from "../../lib/api";
import { IdempotencyKeyManager } from "../../lib/idempotency";
import type { SafeAuthUser } from "../auth/authState";
import { FinanceWorkspace } from "../finance/FinanceWorkspace";
import { OnboardingFlow } from "../onboarding/OnboardingFlow";

interface HouseholdShellProps {
  api: APIClient;
  authUser: SafeAuthUser;
  sessionVersion: number;
  offline: boolean;
  onLogout: () => Promise<void>;
  onSessionExpired: () => void;
}

type LoadState = "loading" | "ready" | "error";

export function HouseholdShell({ api, authUser, sessionVersion, offline, onLogout, onSessionExpired }: HouseholdShellProps) {
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [households, setHouseholds] = useState<Household[]>([]);
  const [activeID, setActiveID] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [profileName, setProfileName] = useState("");
  const [profileSaving, setProfileSaving] = useState(false);
  const [initialFinanceSection, setInitialFinanceSection] = useState<"dashboard" | "transactions">("dashboard");
  const createKeys = useRef(new IdempotencyKeyManager());
  const bootstrapController = useRef<AbortController | null>(null);

  const activeHousehold = useMemo(
    () => households.find((household) => household.id === activeID) ?? null,
    [activeID, households],
  );

  const handleError = useCallback((error: unknown, fallback: string) => {
    if (error instanceof APIError && error.kind === "session_expired") {
      onSessionExpired();
      return;
    }
    setMessage(error instanceof APIError ? error.message : fallback);
  }, [onSessionExpired]);

  const loadBootstrap = useCallback(async () => {
    bootstrapController.current?.abort();
    const controller = new AbortController();
    bootstrapController.current = controller;
    setLoadState("loading");
    setMessage(null);
    setProfile(null);
    setHouseholds([]);
    setActiveID(null);
    try {
      const result = await api.bootstrap(controller.signal);
      if (controller.signal.aborted) return;
      setProfile(result.user);
      setProfileName(result.user.displayName);
      setHouseholds(result.households);
      let rememberedID: string | null = null;
      try { rememberedID = localStorage.getItem(`monetka-active-household-${result.user.id}`); } catch { /* private mode */ }
      setActiveID(result.households.some((household) => household.id === rememberedID) ? rememberedID : result.households[0]?.id ?? null);
      setLoadState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
      setLoadState("error");
      handleError(error, "Не удалось подготовить профиль.");
    } finally {
      if (bootstrapController.current === controller) bootstrapController.current = null;
    }
  }, [api, handleError]);

  useEffect(() => {
    void loadBootstrap();
    return () => bootstrapController.current?.abort();
  }, [loadBootstrap, sessionVersion]);

  useEffect(() => {
    if (!profile || !activeID) return;
    try { localStorage.setItem(`monetka-active-household-${profile.id}`, activeID); } catch { /* private mode */ }
  }, [activeID, profile]);

  const refresh = async () => {
    if (loadState !== "ready" || refreshing || offline) return;
    setRefreshing(true);
    setMessage(null);
    try {
      const next = await api.listHouseholds();
      setHouseholds(next);
      setActiveID((current) => next.some((household) => household.id === current) ? current : next[0]?.id ?? null);
      setLoadState("ready");
    } catch (error) {
      handleError(error, "Не удалось обновить пространства.");
    } finally {
      setRefreshing(false);
    }
  };

  const createHousehold = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (loadState !== "ready" || creating || offline) return;
    const normalizedName = name.trim();
    if (normalizedName.length < 2 || normalizedName.length > 120) {
      setMessage("Название должно содержать от 2 до 120 символов.");
      return;
    }
    setCreating(true);
    setMessage(null);
    try {
      const created = await api.createHousehold(normalizedName, createKeys.current.forPayload(normalizedName));
      createKeys.current.succeeded();
      setHouseholds((current) => [...current.filter((item) => item.id !== created.id), created]);
      setActiveID(created.id);
      setName("");
    } catch (error) {
      handleError(error, "Не удалось создать пространство.");
    } finally {
      setCreating(false);
    }
  };

  const logout = async () => {
    if (loggingOut) return;
    setLoggingOut(true);
    setMessage(null);
    try {
      await onLogout();
    } catch {
      setMessage(offline ? "Нет соединения с интернетом." : "Не удалось завершить сессию.");
    } finally {
      setLoggingOut(false);
    }
  };

  const saveProfile = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!profile || profileSaving || offline) return;
    const normalized = profileName.trim();
    if (!normalized || [...normalized].length > 120) {
      setMessage("Имя должно содержать от 1 до 120 символов.");
      return;
    }
    setProfileSaving(true);
    setMessage(null);
    try {
      const updated = await api.updateProfile({ displayName: normalized, primaryCurrencyCode: profile.primaryCurrencyCode });
      setProfile(updated);
      setProfileName(updated.displayName);
      setProfileOpen(false);
    } catch (error) {
      handleError(error, "Не удалось сохранить профиль.");
    } finally {
      setProfileSaving(false);
    }
  };

  if (loadState === "ready" && profile && !profile.onboardingCompleted) {
    return <OnboardingFlow api={api} profile={profile} offline={offline} onSessionExpired={onSessionExpired} onComplete={(nextProfile, household, destination) => {
      setProfile(nextProfile);
      setProfileName(nextProfile.displayName);
      setHouseholds((current) => [...current.filter((item) => item.id !== household.id), household]);
      setActiveID(household.id);
      setInitialFinanceSection(destination);
    }} />;
  }

  return (
    <main className="workspace-shell">
      <header className="workspace-header">
        <div className="brand-lockup"><span className="brand-mark" aria-hidden="true">₽</span><div><strong>Монетка</strong><span>Ваши финансовые пространства</span></div></div>
        <div className="account-menu"><button className="profile-trigger" type="button" title={authUser.email} onClick={() => setProfileOpen((current) => !current)} aria-expanded={profileOpen}>{profile?.displayName ?? authUser.email}</button><button type="button" onClick={() => void logout()} disabled={loggingOut}>{loggingOut ? "Выходим…" : "Выйти"}</button></div>
      </header>

      {profileOpen && profile && <section className="profile-panel" aria-labelledby="profile-title"><div className="profile-panel__heading"><div><p className="eyebrow">Настройки</p><h2 id="profile-title">Профиль</h2></div><button className="icon-button" type="button" onClick={() => setProfileOpen(false)} aria-label="Закрыть профиль">×</button></div><form onSubmit={(event) => void saveProfile(event)}><label>Имя<input autoFocus value={profileName} onChange={(event) => setProfileName(event.target.value)} maxLength={120} required disabled={profileSaving} /></label><label>Email<input value={authUser.email} readOnly aria-readonly="true" /></label><label>Основная валюта<select value={profile.primaryCurrencyCode} disabled><option value="RUB">Российский рубль (₽)</option></select></label><label>Формат использования<input value={profile.usageMode === "personal" ? "Только для себя" : profile.usageMode === "couple" ? "Вместе с партнёром" : profile.usageMode === "family" ? "Вместе с семьёй" : "Другой вариант"} readOnly /></label><button className="primary-button" type="submit" disabled={profileSaving || offline}>{profileSaving ? "Сохраняем…" : "Сохранить"}</button></form></section>}

      <div className="workspace-grid">
        <aside className="household-sidebar" aria-label="Семейные пространства">
          <div className="sidebar-heading"><div><p className="eyebrow">Пространства</p><h1>Ваши дома</h1></div><button className="icon-button" type="button" onClick={() => void refresh()} disabled={loadState !== "ready" || refreshing || offline} aria-label="Обновить список">{refreshing ? "…" : "↻"}</button></div>
          {loadState === "loading" && <div className="skeleton-list" aria-label="Загрузка пространств" aria-busy="true"><span /><span /><span /></div>}
          {loadState === "ready" && households.length === 0 && <div className="empty-list"><strong>Пока пусто</strong><p>Создайте первое пространство для своей семьи.</p></div>}
          {households.length > 0 && <ul className="household-list">{households.map((household) => <li key={household.id}><button type="button" data-active={household.id === activeID} onClick={() => setActiveID(household.id)}><span>{household.name.slice(0, 1).toUpperCase()}</span><div><strong>{household.name}</strong><small>{household.role === "owner" ? "Владелец" : household.role === "admin" ? "Администратор" : "Участник"}</small></div></button></li>)}</ul>}
        </aside>

        <section className="workspace-content" aria-live="polite">
          {offline && <div className="offline-banner" role="status">Вы офлайн. Изменения временно недоступны.</div>}
          {message && <div className="inline-alert" role="alert"><span>{message}</span><button type="button" onClick={() => setMessage(null)} aria-label="Закрыть сообщение">×</button></div>}
          {loadState === "loading" && <div className="state-card" aria-busy="true"><p className="eyebrow">Подготовка профиля</p><h2>Открываем ваши пространства…</h2><p>Проверяем доступ через защищённый Go API.</p></div>}
          {loadState === "error" && <div className="state-card"><p className="eyebrow">Соединение прервано</p><h2>Не удалось открыть пространства</h2><p>Проверьте Go API и подключение к сети.</p><button className="secondary-button" type="button" onClick={() => void loadBootstrap()} disabled={offline}>Повторить bootstrap</button></div>}
          {loadState === "ready" && (
            <>
              {activeHousehold ? (
                <FinanceWorkspace key={`${activeHousehold.id}-${initialFinanceSection}`} api={api} household={activeHousehold} offline={offline} onSessionExpired={onSessionExpired} initialSection={initialFinanceSection} />
              ) : (
                <div className="welcome-card"><div><span className="welcome-card__mark" aria-hidden="true">⌂</span><p className="eyebrow">Первое пространство</p><h3>Начните с названия</h3><p>Создатель становится владельцем и сможет настроить счета, категории и операции.</p></div></div>
              )}
              <details className="household-creator" open={!activeHousehold}>
                <summary>{activeHousehold ? "Создать ещё одно пространство" : "Новое семейное пространство"}</summary>
                <form className="create-household" onSubmit={(event) => void createHousehold(event)}>
                  <div><label htmlFor="household-name">Название пространства</label><p>Например, «Дом» или «Семья»</p></div>
                  <div className="inline-field"><input id="household-name" value={name} onChange={(event) => setName(event.target.value)} minLength={2} maxLength={120} disabled={creating || offline} required /><button className="primary-button" type="submit" disabled={creating || offline}>{creating ? "Создаём…" : "Создать"}</button></div>
                </form>
              </details>
            </>
          )}
        </section>
      </div>
    </main>
  );
}
