import * as React from "react";
import { AuthProvider as OidcAuthProvider, useAuth as useOidcAuth, hasAuthParams } from "react-oidc-context";
import { WebStorageStateStore, type User } from "oidc-client-ts";
import { OpenAPI } from "@/client";

// OIDC Configuration with sensible defaults for local development
const keycloakUrl = import.meta.env.VITE_KEYCLOAK_URL || "http://localhost:8081";
const keycloakRealm = import.meta.env.VITE_KEYCLOAK_REALM || "memory-service";
const keycloakClientId = import.meta.env.VITE_KEYCLOAK_CLIENT_ID || "frontend";

// Key for storing pre-login URL in sessionStorage
const PRE_LOGIN_URL_KEY = "auth_pre_login_url";

/**
 * Save the current URL before redirecting to the IdP.
 * This allows us to restore query params (like conversationId) after login.
 */
function savePreLoginUrl() {
  if (typeof window !== "undefined" && window.location.search) {
    sessionStorage.setItem(PRE_LOGIN_URL_KEY, window.location.href);
  }
}

/**
 * Restore the pre-login URL after successful authentication.
 * Returns the URL to navigate to, or null if no saved URL.
 */
function getAndClearPreLoginUrl(): string | null {
  if (typeof window === "undefined") return null;
  const savedUrl = sessionStorage.getItem(PRE_LOGIN_URL_KEY);
  sessionStorage.removeItem(PRE_LOGIN_URL_KEY);
  return savedUrl;
}

const oidcConfig = {
  authority: `${keycloakUrl}/realms/${keycloakRealm}`,
  client_id: keycloakClientId,
  redirect_uri: typeof window !== "undefined" ? window.location.origin : "",
  post_logout_redirect_uri: typeof window !== "undefined" ? window.location.origin : "",
  scope: "openid profile email",
  // Use localStorage to persist tokens across browser sessions
  userStore: typeof window !== "undefined" ? new WebStorageStateStore({ store: window.localStorage }) : undefined,
  // Disable automatic silent renewal - we handle renewal on-demand when API calls are made
  // This ensures tokens are only renewed when the user is actively using the app
  automaticSilentRenew: false,
  onSigninCallback: () => {
    // Restore the pre-login URL if we saved one (to preserve query params like conversationId)
    const savedUrl = getAndClearPreLoginUrl();
    if (savedUrl) {
      // Use the saved URL (which has the original query params)
      const url = new URL(savedUrl);
      const newUrl = url.pathname + (url.searchParams.toString() ? "?" + url.searchParams.toString() : "");
      window.history.replaceState({}, document.title, newUrl);
    } else {
      // No saved URL - just clean up OIDC params from current URL
      window.history.replaceState({}, document.title, window.location.pathname);
    }
  },
};

// Check if OIDC is configured (set VITE_KEYCLOAK_URL="" to disable)
const isOidcConfigured = keycloakUrl !== "";

// Auth context type
export interface AuthUser {
  userId: string;
  email?: string;
  name?: string;
}

interface AuthContextValue {
  isAuthenticated: boolean;
  isLoading: boolean;
  error: Error | null;
  user: AuthUser | null;
  accessToken: string | null;
  login: () => void;
  logout: () => void;
  clearSessionAndLogin: () => Promise<void>;
}

const AuthContext = React.createContext<AuthContextValue | undefined>(undefined);

// Mock user for development when OIDC is not configured
const mockUser: AuthUser = {
  userId: "dev-user",
  email: "dev@example.com",
  name: "Dev User",
};

// Extract user info from OIDC User object
function extractUser(user: User | null | undefined): AuthUser | null {
  if (!user?.profile) return null;
  return {
    userId: user.profile.preferred_username || user.profile.sub || "unknown",
    email: user.profile.email,
    name: user.profile.name,
  };
}

// Module-level state for token management
let currentAccessToken: string | undefined;
let tokenExpiresAt: number | undefined; // Unix timestamp in seconds
let silentRenewFn: (() => Promise<User | null>) | undefined;
let onAuthFailureFn: (() => void) | undefined;

// Buffer time before expiration to trigger renewal (60 seconds)
const TOKEN_EXPIRY_BUFFER_SECONDS = 60;

// Flag to prevent concurrent renewal attempts
let isRenewing = false;

// Token resolver function - checks expiration and renews on-demand
// This is called by the OpenAPI client before each API request
const tokenResolver = async (): Promise<string> => {
  const now = Math.floor(Date.now() / 1000);

  // Check if token is expired or about to expire
  if (tokenExpiresAt && now >= tokenExpiresAt - TOKEN_EXPIRY_BUFFER_SECONDS) {
    // Token expired or expiring soon - attempt silent renewal
    if (silentRenewFn && !isRenewing) {
      isRenewing = true;
      try {
        console.info("[Auth] Token expiring, attempting silent renewal...");
        const user = await silentRenewFn();
        if (user?.access_token) {
          currentAccessToken = user.access_token;
          tokenExpiresAt = user.expires_at;
          console.info("[Auth] Token renewed successfully");
          return user.access_token;
        }
        // Renewal returned no user - auth failed
        console.warn("[Auth] Silent renewal returned no user");
        clearAuthState();
        onAuthFailureFn?.();
        return "";
      } catch (error) {
        console.error("[Auth] Silent renewal failed:", error);
        clearAuthState();
        onAuthFailureFn?.();
        return "";
      } finally {
        isRenewing = false;
      }
    } else if (isRenewing) {
      // Another renewal is in progress, return current token and let that complete
      return currentAccessToken ?? "";
    } else {
      // No renewal function available and token expired
      console.warn("[Auth] Token expired and no renewal function available");
      clearAuthState();
      onAuthFailureFn?.();
      return "";
    }
  }

  return currentAccessToken ?? "";
};

// Clear all auth state
function clearAuthState() {
  currentAccessToken = undefined;
  tokenExpiresAt = undefined;
}

/**
 * Get the current access token for direct fetch calls.
 * Returns undefined if no token is available.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function getAccessToken(): string | undefined {
  return currentAccessToken;
}

// Internal provider that uses OIDC
function OidcAuthContextProvider({ children }: { children: React.ReactNode }) {
  const auth = useOidcAuth();
  const [, forceUpdate] = React.useReducer((x) => x + 1, 0);

  // Auto sign-in if we have auth params (returning from IdP)
  const { signinRedirect, signinSilent, removeUser, isAuthenticated, isLoading, activeNavigator } = auth;
  React.useEffect(() => {
    if (!isAuthenticated && !isLoading && !activeNavigator && hasAuthParams()) {
      signinRedirect();
    }
  }, [isAuthenticated, isLoading, activeNavigator, signinRedirect]);

  // Store the silent renewal function for on-demand token refresh
  React.useEffect(() => {
    silentRenewFn = signinSilent;
    // Store the auth failure handler - removes user and forces re-render to show login
    onAuthFailureFn = () => {
      removeUser();
      forceUpdate();
    };
    return () => {
      silentRenewFn = undefined;
      onAuthFailureFn = undefined;
    };
  }, [signinSilent, removeUser]);

  // Track token expiration time for on-demand renewal
  React.useEffect(() => {
    if (auth.user?.expires_at) {
      tokenExpiresAt = auth.user.expires_at;
    }
  }, [auth.user?.expires_at]);

  const clearSessionAndLogin = async () => {
    // Clear the auth state before redirecting to prevent 401 loops
    clearAuthState();
    await auth.removeUser();
    savePreLoginUrl();
    await auth.signinRedirect();
  };

  const value: AuthContextValue = {
    isAuthenticated: auth.isAuthenticated,
    isLoading: auth.isLoading,
    error: auth.error || null,
    user: extractUser(auth.user),
    accessToken: auth.user?.access_token || null,
    login: () => {
      // Save current URL before redirecting so we can restore query params after login
      savePreLoginUrl();
      auth.signinRedirect();
    },
    logout: () => auth.signoutRedirect(),
    clearSessionAndLogin,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

// Mock provider for development
function MockAuthProvider({ children }: { children: React.ReactNode }) {
  const value: AuthContextValue = {
    isAuthenticated: true,
    isLoading: false,
    error: null,
    user: mockUser,
    accessToken: "mock-token",
    login: () => console.log("Mock login"),
    logout: () => console.log("Mock logout"),
    clearSessionAndLogin: async () => console.log("Mock clear session and login"),
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

// Main Auth Provider - uses OIDC if configured, mock otherwise
export function AuthProvider({ children }: { children: React.ReactNode }) {
  if (!isOidcConfigured) {
    console.info("[Auth] OIDC not configured, using mock authentication");
    return <MockAuthProvider>{children}</MockAuthProvider>;
  }

  return (
    <OidcAuthProvider {...oidcConfig}>
      <OidcAuthContextProvider>{children}</OidcAuthContextProvider>
    </OidcAuthProvider>
  );
}

// Hook to use auth context
// eslint-disable-next-line react-refresh/only-export-components
export function useAuth() {
  const context = React.useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
}

// Component to require authentication
interface RequireAuthProps {
  children: React.ReactNode;
  fallback?: React.ReactNode;
}

export function RequireAuth({ children, fallback }: RequireAuthProps) {
  const auth = useAuth();
  const [tokenConfigured, setTokenConfigured] = React.useState(false);

  // Configure token for API calls using useLayoutEffect (runs synchronously before paint)
  // This ensures the token is available before child components make API calls
  React.useLayoutEffect(() => {
    if (auth.isAuthenticated && auth.accessToken) {
      currentAccessToken = auth.accessToken;
      OpenAPI.TOKEN = tokenResolver;
      setTokenConfigured(true);
    } else {
      // Clear token on logout or when not authenticated
      clearAuthState();
      OpenAPI.TOKEN = undefined;
      setTokenConfigured(false);
    }
  }, [auth.isAuthenticated, auth.accessToken]);

  if (auth.error) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center bg-cream">
        <div className="max-w-md p-8 text-center">
          <h1 className="mb-2 font-serif text-2xl text-ink">Authentication Service Unavailable</h1>
          <p className="mb-6 text-stone">Unable to connect to the authentication server. Please try again later.</p>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-full bg-ink px-4 py-2 text-sm font-medium text-cream transition-colors hover:bg-ink/90"
          >
            Try Again
          </button>
          <p className="mt-4 text-xs text-stone">Error: {auth.error.message}</p>
        </div>
      </div>
    );
  }

  if (auth.isLoading) {
    return (
      fallback || (
        <div className="flex min-h-screen items-center justify-center bg-cream">
          <div className="text-stone">Loading...</div>
        </div>
      )
    );
  }

  if (!auth.isAuthenticated) {
    // Trigger login redirect
    auth.login();
    return (
      fallback || (
        <div className="flex min-h-screen items-center justify-center bg-cream">
          <div className="text-stone">Redirecting to login...</div>
        </div>
      )
    );
  }

  // Wait for token to be configured before rendering children
  if (!tokenConfigured) {
    return (
      fallback || (
        <div className="flex min-h-screen items-center justify-center bg-cream">
          <div className="text-stone">Configuring session...</div>
        </div>
      )
    );
  }

  return <>{children}</>;
}
