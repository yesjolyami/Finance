import { useEffect, useMemo, useState, type FormEvent } from "react";

import { APIClient, APIError, type Household, type UsageMode, type UserProfile } from "../../lib/api";
import { FinanceAPI, type AccountType } from "../../lib/financeApi";

interface OnboardingFlowProps {
  api: APIClient;
  profile: UserProfile;
  offline: boolean;
  onSessionExpired: () => void;
  onComplete: (profile: UserProfile, household: Household, destination: "dashboard" | "transactions") => void;
}

interface Draft {
  step: number;
  displayName: string;
  usageMode: UsageMode;
  householdName: string;
  currencyCode: "RUB";
  createAccount: boolean;
  accountName: string;
  bankLabel: string;
  accountType: AccountType;
  initialBalance: string;
  color: string;
  householdKey: string;
  accountKey: string;
  balanceKey: string;
}

const usageOptions: Array<{ value: UsageMode; title: string; description: string }> = [
  { value: "personal", title: "Только для себя", description: "Личное пространство без совместного доступа" },
  { value: "couple", title: "Вместе с партнёром", description: "Общий бюджет для двоих" },
  { value: "family", title: "Вместе с семьёй", description: "Единое семейное пространство" },
  { value: "custom", title: "Другой вариант", description: "Название и формат под вашу задачу" },
];

const defaultNames: Record<UsageMode, string> = {
  personal: "Личные финансы",
  couple: "Наш бюджет",
  family: "Семейный бюджет",
  custom: "Новое пространство",
};

function newKey(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`;
}

function initialDraft(profile: UserProfile): Draft {
  return {
    step: 0,
    displayName: profile.displayName === "Пользователь" ? "" : profile.displayName,
    usageMode: profile.usageMode,
    householdName: defaultNames[profile.usageMode],
    currencyCode: "RUB",
    createAccount: true,
    accountName: "Основная карта",
    bankLabel: "",
    accountType: "regular",
    initialBalance: "0",
    color: "#5F714D",
    householdKey: newKey("onboarding-household"),
    accountKey: newKey("onboarding-account"),
    balanceKey: newKey("onboarding-balance"),
  };
}

function restoreDraft(profile: UserProfile): Draft {
  const fallback = initialDraft(profile);
  try {
    const raw = localStorage.getItem(`monetka-onboarding-${profile.id}`);
    if (!raw) return fallback;
    const value = JSON.parse(raw) as Partial<Draft>;
    if (!value.householdKey || !value.accountKey || !value.balanceKey) return fallback;
    return { ...fallback, ...value, step: Math.max(0, Math.min(4, Number(value.step) || 0)) };
  } catch {
    return fallback;
  }
}

function amountToCents(value: string): number | null {
  const normalized = value.trim().replace(/\s/g, "").replace(",", ".");
  if (!/^-?\d+(?:\.\d{1,2})?$/.test(normalized)) return null;
  const cents = Math.round(Number(normalized) * 100);
  return Number.isSafeInteger(cents) && Math.abs(cents) <= 9_000_000_000_000_000 ? cents : null;
}

function errorMessage(error: unknown): string {
  if (error instanceof APIError) {
    if (error.kind === "offline") return "Нет соединения с интернетом. Введённые данные сохранены.";
    if (error.kind === "timeout") return "Сервер не ответил вовремя. Попробуйте ещё раз — повторные сущности не создадутся.";
    return error.message;
  }
  return "Не удалось завершить настройку. Введённые данные сохранены.";
}

export function OnboardingFlow({ api, profile, offline, onSessionExpired, onComplete }: OnboardingFlowProps) {
  const [draft, setDraft] = useState(() => restoreDraft(profile));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ profile: UserProfile; household: Household } | null>(null);
  const finance = useMemo(() => new FinanceAPI(api), [api]);

  useEffect(() => {
    try { localStorage.setItem(`monetka-onboarding-${profile.id}`, JSON.stringify(draft)); } catch { /* private mode */ }
  }, [draft, profile.id]);

  const update = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));

  const next = async (event: FormEvent) => {
    event.preventDefault();
    if (pending) return;
    setError(null);
    if (draft.step === 0) {
      const name = draft.displayName.trim();
      if (!name || [...name].length > 120) return setError("Введите имя длиной до 120 символов.");
      setPending(true);
      try {
        await api.updateProfile({ displayName: name });
        setDraft((current) => ({ ...current, displayName: name, step: 1 }));
      } catch (requestError) {
        if (requestError instanceof APIError && requestError.kind === "session_expired") onSessionExpired();
        else setError(errorMessage(requestError));
      } finally { setPending(false); }
      return;
    }
    if (draft.step === 1) {
      setPending(true);
      try {
        await api.updateProfile({ usageMode: draft.usageMode });
        setDraft((current) => ({ ...current, step: 2 }));
      } catch (requestError) {
        if (requestError instanceof APIError && requestError.kind === "session_expired") onSessionExpired();
        else setError(errorMessage(requestError));
      } finally { setPending(false); }
      return;
    }
    if (draft.step === 2) {
      const name = draft.householdName.trim();
      if ([...name].length < 2 || [...name].length > 120) return setError("Название должно содержать от 2 до 120 символов.");
      setDraft((current) => ({ ...current, householdName: name, step: 3 }));
      return;
    }
    if (draft.step === 3) {
      setDraft((current) => ({ ...current, step: 4 }));
      return;
    }
    await finish();
  };

  const finish = async () => {
    if (offline || pending) return;
    if (draft.createAccount && (!draft.accountName.trim() || [...draft.accountName.trim()].length > 120)) {
      setError("Введите название счёта длиной до 120 символов.");
      return;
    }
    const balanceCents = amountToCents(draft.initialBalance);
    if (draft.createAccount && balanceCents === null) {
      setError("Введите начальный баланс в формате 15000 или 15000,50.");
      return;
    }
    setPending(true);
    setError(null);
    try {
      const household = await api.createHousehold(draft.householdName, draft.householdKey);
      if (draft.createAccount) {
        const account = (await finance.createAccount(household.id, {
          name: draft.accountName.trim(), color: draft.color, sortOrder: 0,
          accountType: draft.accountType, bankLabel: draft.bankLabel.trim(),
          legacyOwnerLabel: "", ownerUserId: null,
        }, draft.accountKey)).resource;
        if (balanceCents && balanceCents !== 0) {
          await finance.createTransaction(household.id, {
            type: balanceCents > 0 ? "income" : "expense",
            accountId: account.id,
            toAccountId: null,
            categoryId: null,
            amountCents: String(Math.abs(balanceCents)),
            eventDate: new Date().toLocaleDateString("sv-SE"),
            note: "Начальный баланс",
            isBalanceAdjustment: true,
          }, draft.balanceKey);
        }
      }
      const updatedProfile = await api.updateProfile({
        displayName: draft.displayName,
        usageMode: draft.usageMode,
        primaryCurrencyCode: draft.currencyCode,
        onboardingCompleted: true,
      });
      try { localStorage.removeItem(`monetka-onboarding-${profile.id}`); } catch { /* private mode */ }
      setDraft((current) => ({ ...current, step: 5 }));
      setResult({ profile: updatedProfile, household });
    } catch (requestError) {
      if (requestError instanceof APIError && requestError.kind === "session_expired") onSessionExpired();
      else setError(errorMessage(requestError));
    } finally { setPending(false); }
  };

  const chooseMode = (mode: UsageMode) => {
    setDraft((current) => ({
      ...current,
      usageMode: mode,
      householdName: Object.values(defaultNames).includes(current.householdName) ? defaultNames[mode] : current.householdName,
    }));
  };

  return (
    <main className="onboarding-shell">
      <section className="onboarding-card" aria-labelledby="onboarding-title">
        <header className="onboarding-header">
          <div className="brand-lockup"><span className="brand-mark" aria-hidden="true">₽</span><div><strong>Монетка</strong><span>Первичная настройка</span></div></div>
          <span className="step-count">{result ? "Настройка завершена" : `Шаг ${draft.step + 1} из 5`}</span>
        </header>
        <div className="step-progress" data-step={result ? "complete" : String(draft.step + 1)} aria-hidden="true"><span /></div>
        {result ? <div className="onboarding-complete"><span className="onboarding-complete__mark" aria-hidden="true">✓</span><p className="eyebrow">Добро пожаловать в «Монетку», {result.profile.displayName}</p><h1 id="onboarding-title">Всё готово</h1><p className="lead">Теперь можно добавить первую операцию и начать следить за финансами в «Монетке».</p><div className="onboarding-actions"><button className="secondary-button" type="button" onClick={() => onComplete(result.profile, result.household, "dashboard")}>Перейти на главную</button><button className="primary-button" type="button" onClick={() => onComplete(result.profile, result.household, "transactions")}>Добавить первую операцию</button></div></div> :
        <form className="onboarding-form" onSubmit={(event) => void next(event)}>
          {draft.step === 0 && <div className="onboarding-step"><p className="eyebrow">Знакомство</p><h1 id="onboarding-title">Как к вам обращаться?</h1><p className="lead">Имя будет видно в профиле, операциях и совместных пространствах.</p><label htmlFor="onboarding-name">Ваше имя</label><input id="onboarding-name" autoFocus autoComplete="name" value={draft.displayName} onChange={(event) => update("displayName", event.target.value)} maxLength={120} disabled={pending} required placeholder="Например, Матвей" /></div>}
          {draft.step === 1 && <div className="onboarding-step"><p className="eyebrow">Формат использования</p><h1 id="onboarding-title">Как вы будете вести финансы?</h1><div className="choice-grid">{usageOptions.map((option) => <button key={option.value} type="button" className="choice-card" data-active={draft.usageMode === option.value} aria-pressed={draft.usageMode === option.value} onClick={() => chooseMode(option.value)} disabled={pending}><strong>{option.title}</strong><span>{option.description}</span></button>)}</div></div>}
          {draft.step === 2 && <div className="onboarding-step"><p className="eyebrow">Ваше пространство</p><h1 id="onboarding-title">Как его назвать?</h1><p className="lead">Внутри пространства будут храниться счета, категории и операции.</p><label htmlFor="household-title">Название пространства</label><input id="household-title" autoFocus value={draft.householdName} onChange={(event) => update("householdName", event.target.value)} minLength={2} maxLength={120} required /></div>}
          {draft.step === 3 && <div className="onboarding-step"><p className="eyebrow">Основная валюта</p><h1 id="onboarding-title">Выберите валюту</h1><p className="lead">Позже здесь появятся другие валюты. Сейчас все расчёты ведутся в рублях.</p><button className="currency-card" type="button" aria-pressed="true"><span>₽</span><div><strong>Российский рубль</strong><small>RUB · основная валюта</small></div><b>Выбрано</b></button></div>}
          {draft.step === 4 && <div className="onboarding-step onboarding-step--wide"><p className="eyebrow">Первый счёт</p><h1 id="onboarding-title">Добавим счёт?</h1><label className="skip-toggle"><input type="checkbox" checked={draft.createAccount} onChange={(event) => update("createAccount", event.target.checked)} /><span><strong>Создать первый счёт</strong><small>Можно пропустить и сделать это позже</small></span></label>{draft.createAccount && <div className="account-setup-grid"><label>Название<input value={draft.accountName} onChange={(event) => update("accountName", event.target.value)} maxLength={120} required /></label><label>Банк <small>необязательно</small><input value={draft.bankLabel} onChange={(event) => update("bankLabel", event.target.value)} maxLength={120} /></label><label>Тип<select value={draft.accountType} onChange={(event) => update("accountType", event.target.value as AccountType)}><option value="regular">Обычный счёт</option><option value="savings">Накопительный счёт</option><option value="cash">Наличные</option></select></label><label>Начальный баланс<input inputMode="decimal" value={draft.initialBalance} onChange={(event) => update("initialBalance", event.target.value)} /></label><fieldset className="onboarding-colors"><legend>Цвет</legend>{["#5F714D", "#A97A39", "#934952", "#4D6671", "#65558F"].map((color) => <button key={color} type="button" data-color={color} data-active={draft.color === color} aria-label={`Цвет ${color}`} aria-pressed={draft.color === color} onClick={() => update("color", color)} />)}</fieldset></div>}</div>}
          {error && <div className="inline-alert" role="alert"><span>{error}</span></div>}
          <footer className="onboarding-actions">{draft.step > 0 && <button className="secondary-button" type="button" onClick={() => update("step", draft.step - 1)} disabled={pending}>Назад</button>}<button className="primary-button" type="submit" disabled={pending || offline}>{pending ? "Сохраняем…" : draft.step === 4 ? "Завершить настройку" : "Продолжить"}</button></footer>
        </form>
        }
      </section>
    </main>
  );
}
