export interface SafeAuthUser {
  id: string;
  email: string;
}

export type AuthPhase = "loading" | "signedOut" | "authenticated" | "recovery";

export interface AuthState {
  phase: AuthPhase;
  user: SafeAuthUser | null;
  sessionVersion: number;
  notice: string | null;
}

export type AuthAction =
  | { type: "initial"; user: SafeAuthUser | null }
  | { type: "signedIn"; user: SafeAuthUser }
  | { type: "sessionUpdated"; user: SafeAuthUser | null }
  | { type: "passwordRecovery"; user: SafeAuthUser }
  | { type: "signedOut" }
  | { type: "sessionExpired" }
  | { type: "notice"; message: string | null };

export const initialAuthState: AuthState = {
  phase: "loading",
  user: null,
  sessionVersion: 0,
  notice: null,
};

export function authReducer(state: AuthState, action: AuthAction): AuthState {
  switch (action.type) {
    case "initial":
      return action.user
        ? { phase: "authenticated", user: action.user, sessionVersion: state.sessionVersion + 1, notice: null }
        : { phase: "signedOut", user: null, sessionVersion: state.sessionVersion, notice: null };
    case "signedIn":
      if (state.phase === "authenticated" && state.user?.id === action.user.id) return state;
      return { phase: "authenticated", user: action.user, sessionVersion: state.sessionVersion + 1, notice: null };
    case "sessionUpdated":
      if (state.phase === "recovery") return action.user ? { ...state, user: action.user } : state;
      if (!action.user) {
        return { phase: "signedOut", user: null, sessionVersion: state.sessionVersion, notice: null };
      }
      return {
        phase: "authenticated",
        user: action.user,
        sessionVersion: state.phase === "loading" ? state.sessionVersion + 1 : state.sessionVersion,
        notice: null,
      };
    case "passwordRecovery":
      return { phase: "recovery", user: action.user, sessionVersion: state.sessionVersion, notice: null };
    case "signedOut":
      return { phase: "signedOut", user: null, sessionVersion: state.sessionVersion, notice: state.notice };
    case "sessionExpired":
      return {
        phase: "signedOut",
        user: null,
        sessionVersion: state.sessionVersion,
        notice: "Сессия завершилась. Войдите снова.",
      };
    case "notice":
      return { ...state, notice: action.message };
  }
}

export function authActionForEvent(event: string, user: SafeAuthUser | null): AuthAction | null {
  switch (event) {
    case "INITIAL_SESSION":
      return { type: "initial", user };
    case "SIGNED_IN":
      return user ? { type: "signedIn", user } : { type: "initial", user: null };
    case "PASSWORD_RECOVERY":
      return user ? { type: "passwordRecovery", user } : { type: "initial", user: null };
    case "SIGNED_OUT":
      return { type: "signedOut" };
    case "TOKEN_REFRESHED":
    case "USER_UPDATED":
      return { type: "sessionUpdated", user };
    default:
      return null;
  }
}

export interface AuthLifecycleGate {
  acceptEvent: (action: AuthAction | null) => action is AuthAction;
  acceptInitial: () => boolean;
  deactivate: () => void;
}

export function createAuthLifecycleGate(): AuthLifecycleGate {
  let active = true;
  let authoritativeEventReceived = false;
  return {
    acceptEvent(action): action is AuthAction {
      if (!active || !action) return false;
      authoritativeEventReceived = true;
      return true;
    },
    acceptInitial: () => active && !authoritativeEventReceived,
    deactivate: () => {
      active = false;
    },
  };
}
