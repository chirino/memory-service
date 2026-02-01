import * as React from "react";
import { AuthProvider as OidcAuthProvider, useAuth as useOidcAuth, hasAuthParams } from "react-oidc-context";
import type { User } from "oidc-client-ts";
import { OpenAPI } from "@/client";

// OIDC Configuration with sensible defaults for local development
const keycloakUrl = import.meta.env.VITE_KEYCLOAK_URL || "http://localhost:8081";
const keycloakRealm = import.meta.env.VITE_KEYCLOAK_REALM || "memory-service";
const keycloakClientId = import.meta.env.VITE_KEYCLOAK_CLIENT_ID || "frontend";

const oidcConfig = {
  authority: `${keycloakUrl}/realms/${keycloakRealm}`,
  client_id: keycloakClientId,
  redirect_uri: typeof window !== "undefined" ? window.location.origin : "",
  post_logout_redirect_uri: typeof window !== "undefined" ? window.location.origin : "",
  scope: "openid profile email",
  onSigninCallback: () => {
    // Remove OIDC query params from URL after login
    window.history.replaceState({}, document.title, window.location.pathname);
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

// Store the current access token in a module-level variable for the resolver
let currentAccessToken: string | undefined;

// Token resolver function - always returns the current token
// This is used by the OpenAPI client to get the token at request time
const tokenResolver = async (): Promise<string> => {
  return currentAccessToken ?? "";
};

/**
 * Get the current access token for direct fetch calls.
 * Returns undefined if no token is available.
 */
export function getAccessToken(): string | undefined {
  return currentAccessToken;
}

// Internal provider that uses OIDC
function OidcAuthContextProvider({ children }: { children: React.ReactNode }) {
  const auth = useOidcAuth();

  // Auto sign-in if we have auth params (returning from IdP)
  const { signinRedirect, isAuthenticated, isLoading, activeNavigator } = auth;
  React.useEffect(() => {
    if (!isAuthenticated && !isLoading && !activeNavigator && hasAuthParams()) {
      signinRedirect();
    }
  }, [isAuthenticated, isLoading, activeNavigator, signinRedirect]);

  const value: AuthContextValue = {
    isAuthenticated: auth.isAuthenticated,
    isLoading: auth.isLoading,
    error: auth.error || null,
    user: extractUser(auth.user),
    accessToken: auth.user?.access_token || null,
    login: () => auth.signinRedirect(),
    logout: () => auth.signoutRedirect(),
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
      currentAccessToken = undefined;
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
