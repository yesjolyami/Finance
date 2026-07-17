import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type FormEvent } from "react";

import { APIError, type APIClient } from "../../lib/api";
import {
  BackupImportAPI,
  type BackupConfirmWarningCounts,
  type BackupImportCounts,
  type BackupPreviewWarning,
} from "../../lib/backupImportApi";
import { formatMoney } from "./financeModel";
import {
  BackupConfirmKeyManager,
  ImportOperationGate,
  budgetMonthFromInput,
  currentBudgetMonth,
  isFreshPreview,
  monthInputValue,
  resolveImportError,
  safeConfirmation,
  safePreview,
  validateBackupFile,
  type BackupImportPhase,
  type SafeBackupConfirmation,
  type SafeBackupPreview,
} from "./backupImportModel";

interface BackupImportPanelProps {
  api: APIClient;
  householdID: string;
  offline: boolean;
  onSessionExpired: () => void;
  onImported: () => void;
  onOpenDashboard: () => void;
}

const countLabels: ReadonlyArray<[keyof BackupImportCounts, string]> = [
  ["accounts", "Счета"],
  ["categories", "Категории"],
  ["transactions", "Операции"],
  ["budgets", "Бюджеты"],
  ["goals", "Цели"],
  ["goalContributions", "Пополнения целей"],
  ["debts", "Долги"],
  ["debtPayments", "Платежи по долгам"],
];

const warningLabels: Readonly<Record<BackupPreviewWarning["code"], string>> = {
  legacy_owner_not_linked: "Текстовые владельцы счетов останутся legacy-метками",
  archive_time_approximated: "Дата архивации будет восстановлена приблизительно",
  goal_exceeds_target: "Накопления по отдельным целям превышают целевую сумму",
  debt_overpaid: "Платежи по отдельным долгам превышают исходную сумму",
  system_resource_preserved: "Системные счета или категории будут сохранены",
  budget_month_explicit_choice: "Бюджеты будут привязаны к выбранному месяцу",
};

const confirmWarningLabels: ReadonlyArray<[keyof BackupConfirmWarningCounts, string]> = [
  ["legacyOwnerNotLinked", warningLabels.legacy_owner_not_linked],
  ["archiveTimeApproximated", warningLabels.archive_time_approximated],
  ["goalExceedsTarget", warningLabels.goal_exceeds_target],
  ["debtOverpaid", warningLabels.debt_overpaid],
  ["systemResourcePreserved", warningLabels.system_resource_preserved],
  ["budgetMonthExplicitChoice", warningLabels.budget_month_explicit_choice],
];

function formattedTimestamp(value: string): string {
  const parsed = new Date(value);
  if (!Number.isFinite(parsed.getTime())) return "неизвестно";
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(parsed);
}

function formattedFileSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.ceil(bytes / 1024))} КиБ`;
  return `${(bytes / (1024 * 1024)).toFixed(1).replace(".", ",")} МиБ`;
}

function CountGrid({ counts }: { counts: BackupImportCounts }) {
  return (
    <dl className="import-count-grid">
      {countLabels.map(([key, label]) => (
        <div key={key}>
          <dt>{label}</dt>
          <dd>{counts[key].toLocaleString("ru-RU")}</dd>
        </div>
      ))}
    </dl>
  );
}

function PreviewSummary({ preview }: { preview: SafeBackupPreview }) {
  return (
    <section className="import-preview" aria-labelledby="import-preview-title">
      <div className="import-preview__heading">
        <div>
          <p className="eyebrow">Проверка завершена</p>
          <h4 id="import-preview-title">Состав резервной копии</h4>
        </div>
        <div className="import-preview__meta">
          <span>Месяц {preview.budgetMonth}</span>
          <span>Действует до {formattedTimestamp(preview.expiresAt)}</span>
        </div>
      </div>
      <CountGrid counts={preview.counts} />
      <dl className="import-total-grid">
        <div><dt>Доходы</dt><dd className="money-positive">{formatMoney(preview.totals.incomeCents)}</dd></div>
        <div><dt>Расходы</dt><dd className="money-negative">{formatMoney(preview.totals.expenseCents)}</dd></div>
        <div><dt>Переводы</dt><dd>{formatMoney(preview.totals.transferCents)}</dd></div>
        <div><dt>Итоговый баланс</dt><dd>{formatMoney(preview.totals.householdBalanceCents)}</dd></div>
      </dl>
      <div className="import-warning-list">
        <strong>{preview.warnings.length > 0 ? "Предупреждения проверки" : "Предупреждений нет"}</strong>
        {preview.warnings.length > 0 ? (
          <ul>
            {preview.warnings.map((warning) => (
              <li key={warning.code}>
                <span><code>{warning.code}</code>{warningLabels[warning.code]}</span>
                <b>{warning.count}</b>
              </li>
            ))}
          </ul>
        ) : <p>Структура и связи backup v5 прошли предварительную проверку.</p>}
      </div>
    </section>
  );
}

function ConfirmationSummary({
  result,
  onOpenDashboard,
}: {
  result: SafeBackupConfirmation;
  onOpenDashboard: () => void;
}) {
  const warnings = confirmWarningLabels.filter(([key]) => result.warningCounts[key] > 0);
  return (
    <section className="import-success" aria-labelledby="import-success-title">
      <span className="import-success__mark" aria-hidden="true">✓</span>
      <div>
        <p className="eyebrow">Импорт завершён</p>
        <h4 id="import-success-title">Данные пространства обновлены</h4>
        <p>
          Завершено {formattedTimestamp(result.completedAt)}.
          {result.replayed ? " Сервер подтвердил безопасный повтор исходного запроса." : ""}
        </p>
      </div>
      <CountGrid counts={result.counts} />
      {warnings.length > 0 && (
        <ul className="import-success__warnings">
          {warnings.map(([key, label]) => <li key={key}>{label}: {result.warningCounts[key]}</li>)}
        </ul>
      )}
      <button className="primary-button" type="button" onClick={onOpenDashboard}>Перейти к обзору</button>
    </section>
  );
}

export function BackupImportPanel({
  api,
  householdID,
  offline,
  onSessionExpired,
  onImported,
  onOpenDashboard,
}: BackupImportPanelProps) {
  const importAPI = useMemo(() => new BackupImportAPI(api), [api]);
  const [file, setFile] = useState<File | null>(null);
  const [budgetMonth, setBudgetMonth] = useState(() => currentBudgetMonth());
  const [preview, setPreview] = useState<SafeBackupPreview | null>(null);
  const [confirmation, setConfirmation] = useState<SafeBackupConfirmation | null>(null);
  const [agreed, setAgreed] = useState(false);
  const [phase, setPhase] = useState<BackupImportPhase>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const tokenRef = useRef<string | null>(null);
  const confirmKeys = useRef(new BackupConfirmKeyManager());
  const operationGate = useRef(new ImportOperationGate());
  const controllerRef = useRef<AbortController | null>(null);
  const inputVersionRef = useRef(0);
  const mountedRef = useRef(true);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const clearPreview = useCallback(() => {
    tokenRef.current = null;
    confirmKeys.current.invalidate();
    setPreview(null);
    setAgreed(false);
  }, []);

  const invalidateInput = useCallback(() => {
    inputVersionRef.current += 1;
    controllerRef.current?.abort();
    clearPreview();
    setConfirmation(null);
    setMessage(null);
    setPhase("idle");
  }, [clearPreview]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      inputVersionRef.current += 1;
      controllerRef.current?.abort();
      tokenRef.current = null;
      confirmKeys.current.invalidate();
    };
  }, [householdID]);

  useEffect(() => {
    if (offline) controllerRef.current?.abort();
  }, [offline]);

  useEffect(() => {
    if (!preview) return;
    const delay = Date.parse(preview.expiresAt) - Date.now();
    if (delay <= 0) {
      clearPreview();
      setPhase("idle");
      setMessage("Preview истёк. Выполните проверку файла ещё раз.");
      return;
    }
    const timer = globalThis.setTimeout(() => {
      clearPreview();
      setPhase("idle");
      setMessage("Preview истёк. Выполните проверку файла ещё раз.");
    }, delay);
    return () => globalThis.clearTimeout(timer);
  }, [clearPreview, preview]);

  const selectFile = (event: ChangeEvent<HTMLInputElement>) => {
    invalidateInput();
    const selected = event.currentTarget.files?.[0] ?? null;
    const validation = validateBackupFile(selected);
    if (validation) {
      setFile(null);
      event.currentTarget.value = "";
      setMessage(validation);
      return;
    }
    setFile(selected);
  };

  const selectMonth = (event: ChangeEvent<HTMLInputElement>) => {
    const next = budgetMonthFromInput(event.currentTarget.value);
    if (!next) return;
    invalidateInput();
    setBudgetMonth(next);
  };

  const runPreview = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (offline || !operationGate.current.begin()) return;
    const validation = validateBackupFile(file);
    if (validation || !file) {
      setMessage(validation ?? "Выберите JSON-файл.");
      operationGate.current.end();
      return;
    }

    inputVersionRef.current += 1;
    const inputVersion = inputVersionRef.current;
    controllerRef.current?.abort();
    clearPreview();
    setConfirmation(null);
    setMessage(null);
    setPhase("previewing");
    const controller = new AbortController();
    controllerRef.current = controller;
    try {
      const response = await importAPI.preview(householdID, file, budgetMonth, controller.signal);
      if (!mountedRef.current || inputVersion !== inputVersionRef.current) return;
      if (response.budgetMonth !== budgetMonth || Date.parse(response.expiresAt) <= Date.now()) {
        throw new APIError("invalid_response", 200);
      }
      tokenRef.current = response.confirmationToken;
      confirmKeys.current.previewSucceeded();
      setPreview(safePreview(response));
      setPhase("ready");
    } catch (error) {
      if (!mountedRef.current || inputVersion !== inputVersionRef.current) return;
      if (controller.signal.aborted) {
        setPhase("idle");
        if (offline) setMessage("Нет соединения. Preview не создан.");
        return;
      }
      const resolution = resolveImportError(error, "preview");
      setMessage(resolution.message);
      setPhase("idle");
      if (resolution.sessionExpired) onSessionExpired();
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null;
      operationGate.current.end();
    }
  };

  const runConfirm = async () => {
    if (offline || !operationGate.current.begin()) return;
    const token = tokenRef.current;
    const idempotencyKey = confirmKeys.current.current();
    if (!file || !preview || !token || !idempotencyKey || !agreed || !isFreshPreview(preview)) {
      setMessage("Создайте свежий preview и подтвердите необратимость импорта.");
      if (!isFreshPreview(preview)) clearPreview();
      operationGate.current.end();
      return;
    }

    const inputVersion = inputVersionRef.current;
    setMessage(null);
    setPhase("confirming");
    const controller = new AbortController();
    controllerRef.current = controller;
    try {
      const result = await importAPI.confirm(
        householdID,
        file,
        budgetMonth,
        token,
        idempotencyKey,
        controller.signal,
      );
      if (!mountedRef.current || inputVersion !== inputVersionRef.current) return;
      tokenRef.current = null;
      confirmKeys.current.invalidate();
      setFile(null);
      setPreview(null);
      setAgreed(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
      setConfirmation(safeConfirmation(result.response, result.replayed));
      setPhase("success");
      onImported();
    } catch (error) {
      if (!mountedRef.current || inputVersion !== inputVersionRef.current) return;
      if (controller.signal.aborted) {
        setPhase("ready");
        if (offline) setMessage("Соединение прервано. Безопасно повторите подтверждение после восстановления сети.");
        return;
      }
      const resolution = resolveImportError(error, "confirm");
      setMessage(resolution.message);
      if (resolution.invalidatePreview) {
        clearPreview();
        setPhase("idle");
      } else {
        setPhase("ready");
      }
      if (resolution.sessionExpired) onSessionExpired();
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null;
      operationGate.current.end();
    }
  };

  const pending = phase === "previewing" || phase === "confirming";
  const confirmEnabled = Boolean(
    file && preview && agreed && isFreshPreview(preview) && !offline && !pending,
  );

  return (
    <section className="finance-panel import-panel" aria-labelledby="import-title">
      <div className="panel-toolbar import-toolbar">
        <div>
          <p className="eyebrow">Backup v5</p>
          <h3 id="import-title">Импорт резервной копии</h3>
          <p>Двухфазная проверка перед единственной атомарной записью данных.</p>
        </div>
        <span className="owner-only-badge">Только владелец</span>
      </div>

      <div className="import-safety-note" role="note">
        <span aria-hidden="true">!</span>
        <div>
          <strong>Только для полностью пустого пространства</strong>
          <p>Импорт заполнит пространство данными backup v5. Слияние и замена существующих данных не выполняются, а завершённое действие необратимо.</p>
        </div>
      </div>

      {message && <div className="finance-alert import-alert" role="alert" aria-live="assertive"><span>{message}</span></div>}
      {offline && <div className="permission-note" role="status">Вы офлайн. Preview и подтверждение временно заблокированы.</div>}

      {confirmation ? (
        <ConfirmationSummary result={confirmation} onOpenDashboard={onOpenDashboard} />
      ) : (
        <>
          <form className="import-form" onSubmit={(event) => void runPreview(event)}>
            <label className="import-file-field" htmlFor="backup-v5-file">
              <span className="import-file-field__icon" aria-hidden="true">JSON</span>
              <span>
                <strong>{file ? "JSON-файл выбран" : "Выберите backup v5"}</strong>
                <small>{file ? `Размер ${formattedFileSize(file.size)} · содержимое не отображается` : "Локальный файл до 32 МиБ. Содержимое останется скрытым."}</small>
              </span>
              <input
                ref={fileInputRef}
                id="backup-v5-file"
                type="file"
                accept="application/json,.json"
                onChange={selectFile}
                disabled={pending}
                required
              />
            </label>
            <label className="import-month-field" htmlFor="backup-budget-month">
              Месяц для бюджетов
              <input
                id="backup-budget-month"
                type="month"
                value={monthInputValue(budgetMonth)}
                onChange={selectMonth}
                disabled={pending}
                required
              />
              <small>В API будет передано {budgetMonth}</small>
            </label>
            <button className="secondary-button" type="submit" disabled={!file || pending || offline}>
              {phase === "previewing" ? "Проверяем…" : preview ? "Создать новый preview" : "Проверить файл"}
            </button>
          </form>

          {preview && (
            <>
              <PreviewSummary preview={preview} />
              <div className="import-confirm">
                <label className="import-consent">
                  <input
                    type="checkbox"
                    checked={agreed}
                    onChange={(event) => setAgreed(event.currentTarget.checked)}
                    disabled={pending || offline}
                  />
                  <span>
                    <strong>Я понимаю, что импорт необратим</strong>
                    <small>Файл и выбранный месяц должны оставаться неизменными до завершения.</small>
                  </span>
                </label>
                <button
                  className="danger-button import-confirm__button"
                  type="button"
                  onClick={() => void runConfirm()}
                  disabled={!confirmEnabled}
                >
                  {phase === "confirming" ? "Импортируем…" : "Подтвердить импорт"}
                </button>
              </div>
            </>
          )}
        </>
      )}
      <p className="import-privacy-note">Файл не сохраняется в браузерном хранилище. Секрет подтверждения не отображается и очищается при смене входных данных.</p>
    </section>
  );
}
