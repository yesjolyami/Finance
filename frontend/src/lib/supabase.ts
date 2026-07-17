import { createClient, type SupabaseClient } from "@supabase/supabase-js";

import type { FrontendConfig } from "./config";

let singleton: SupabaseClient | null = null;
let singletonConfiguration = "";

export function getSupabaseClient(config: FrontendConfig): SupabaseClient {
  const configuration = `${config.supabaseUrl}\n${config.supabasePublishableKey}`;
  if (singleton && singletonConfiguration !== configuration) {
    throw new Error("Supabase client configuration cannot change at runtime");
  }
  if (!singleton) {
    singleton = createClient(config.supabaseUrl, config.supabasePublishableKey, {
      auth: {
        autoRefreshToken: true,
        detectSessionInUrl: true,
        persistSession: true,
      },
    });
    singletonConfiguration = configuration;
  }
  return singleton;
}
