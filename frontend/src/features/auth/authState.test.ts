import { describe, expect, it } from "vitest";

import {
  authActionForEvent,
  authReducer,
  createAuthLifecycleGate,
  initialAuthState,
  type AuthState,
  type SafeAuthUser,
} from "./authState";

const user: SafeAuthUser = { id: "11000000-0000-4000-8000-000000000001", email: "person@example.test" };
const refreshedUser: SafeAuthUser = { ...user, email: "updated@example.test" };

describe("auth event mapping and reducer", () => {
  it("does not let an ignored event block the initial session fallback", () => {
    const gate = createAuthLifecycleGate();
    expect(gate.acceptEvent(authActionForEvent("MFA_CHALLENGE_VERIFIED", user))).toBe(false);
    expect(gate.acceptInitial()).toBe(true);
  });

  it("uses token refresh to leave loading and update authenticated safe fields", () => {
    const loadingAction = authActionForEvent("TOKEN_REFRESHED", user);
    expect(loadingAction).not.toBeNull();
    const authenticated = authReducer(initialAuthState, loadingAction!);
    expect(authenticated).toMatchObject({ phase: "authenticated", user, sessionVersion: 1 });

    const refreshAction = authActionForEvent("USER_UPDATED", refreshedUser);
    const refreshed = authReducer(authenticated, refreshAction!);
    expect(refreshed).toMatchObject({ phase: "authenticated", user: refreshedUser, sessionVersion: 1 });
  });

  it("does not throw the user out of password recovery on refresh events", () => {
    const recovery: AuthState = {
      phase: "recovery",
      user,
      sessionVersion: 0,
      notice: null,
    };
    const action = authActionForEvent("TOKEN_REFRESHED", refreshedUser);
    expect(authReducer(recovery, action!)).toMatchObject({ phase: "recovery", user: refreshedUser });
  });

  it("maps password recovery and sign out explicitly", () => {
    const recoveryAction = authActionForEvent("PASSWORD_RECOVERY", user);
    const recovery = authReducer(initialAuthState, recoveryAction!);
    expect(recovery.phase).toBe("recovery");

    const signedOutAction = authActionForEvent("SIGNED_OUT", null);
    expect(authReducer(recovery, signedOutAction!)).toMatchObject({ phase: "signedOut", user: null });
  });

  it("rejects stale initial promises after an authoritative event or cleanup", () => {
    const afterEvent = createAuthLifecycleGate();
    expect(afterEvent.acceptEvent(authActionForEvent("SIGNED_IN", user))).toBe(true);
    expect(afterEvent.acceptInitial()).toBe(false);

    const afterCleanup = createAuthLifecycleGate();
    afterCleanup.deactivate();
    expect(afterCleanup.acceptInitial()).toBe(false);
    expect(afterCleanup.acceptEvent(authActionForEvent("SIGNED_IN", user))).toBe(false);
  });
});
