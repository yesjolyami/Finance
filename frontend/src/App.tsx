import { useMemo } from "react";

import { AuthHouseholdShell } from "./features/auth/AuthHouseholdShell";
import { APIClient } from "./lib/api";
import { frontendConfig, type FrontendConfig } from "./lib/config";
import { getSupabaseClient } from "./lib/supabase";

function ConfigurationScreen({ variables }: { variables: readonly string[] }) {
  return (
    <main className="page-shell page-shell--centered">
      <section className="configuration-card" aria-labelledby="configuration-title">
        <div className="brand-mark" aria-hidden="true">₽</div>
        <p className="eyebrow">Безопасная настройка</p>
        <h1 id="configuration-title">Подключение авторизации не настроено</h1>
        <p className="lead">
          Приложение не будет выполнять auth-запросы, пока публичная конфигурация не станет корректной.
        </p>
        <div className="configuration-note" role="status">
          <strong>Проверьте переменные окружения</strong>
          <ul>
            {variables.map((variable) => <li key={variable}><code>{variable}</code></li>)}
          </ul>
        </div>
        <p className="fine-print">Используйте только Supabase publishable/anon key. Service-role key здесь запрещён.</p>
      </section>
    </main>
  );
}

function ConfiguredApplication({ config }: { config: FrontendConfig }) {
  const supabase = useMemo(() => getSupabaseClient(config), [config]);
  const api = useMemo(
    () => new APIClient({ apiBaseUrl: config.apiBaseUrl, sessionProvider: supabase }),
    [config.apiBaseUrl, supabase],
  );
  return <AuthHouseholdShell api={api} supabase={supabase} />;
}

export function App() {
  if (!frontendConfig.ok) {
    return <ConfigurationScreen variables={frontendConfig.missingOrInvalid} />;
  }
  return <ConfiguredApplication config={frontendConfig.config} />;
}
