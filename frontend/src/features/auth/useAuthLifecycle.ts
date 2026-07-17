import { useEffect, useReducer } from "react";
import type { Session, SupabaseClient, User } from "@supabase/supabase-js";

import {
  authActionForEvent,
  authReducer,
  createAuthLifecycleGate,
  initialAuthState,
  type SafeAuthUser,
} from "./authState";

function safeUser(user: User | undefined): SafeAuthUser | null {
  if (!user?.id) return null;
  return { id: user.id, email: user.email ?? "Аккаунт Supabase" };
}

function userFromSession(session: Session | null): SafeAuthUser | null {
  return safeUser(session?.user);
}

export function useAuthLifecycle(supabase: SupabaseClient) {
  const [state, dispatch] = useReducer(authReducer, initialAuthState);

  useEffect(() => {
    const gate = createAuthLifecycleGate();
    const { data } = supabase.auth.onAuthStateChange((event, session) => {
      const action = authActionForEvent(event, userFromSession(session));
      if (gate.acceptEvent(action)) dispatch(action);
    });

    void supabase.auth.getSession().then(({ data: sessionData, error }) => {
      if (gate.acceptInitial()) {
        dispatch({ type: "initial", user: error ? null : userFromSession(sessionData.session) });
      }
    }).catch(() => {
      if (gate.acceptInitial()) dispatch({ type: "initial", user: null });
    });

    return () => {
      gate.deactivate();
      data.subscription.unsubscribe();
    };
  }, [supabase]);

  return { state, dispatch };
}
