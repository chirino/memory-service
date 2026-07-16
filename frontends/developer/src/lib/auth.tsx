import { createContext, useContext, useLayoutEffect, type ReactNode } from "react";
import { AuthProvider as OidcAuthProvider, useAuth as useOidcAuth } from "react-oidc-context";
import type { AppConfig } from "./config";
import { clearCredentials, setAccessToken, setApiKey } from "../api/client";

interface AuthUser {
  profile: {
    name?: string;
    email?: string;
  };
}

interface AuthContextValue {
  user: AuthUser | null | undefined;
  isLoading: boolean;
  isAuthenticated: boolean;
  login: () => void;
  logout: () => void;
  canLogout: boolean;
  hasRole: (role: string) => boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within AuthProvider");
  }
  return context;
}

interface AuthProviderProps {
  config: AppConfig;
  children: ReactNode;
}

export function AuthProvider({ config, children }: AuthProviderProps) {
  if (config.auth.mode === "api-key") {
    return <ApiKeyAuthContextProvider apiKey={config.auth.apiKey}>{children}</ApiKeyAuthContextProvider>;
  }

  const oidcConfig = {
    authority: config.auth.authority,
    client_id: config.auth.clientId,
    redirect_uri: config.auth.redirectUri,
    post_logout_redirect_uri: config.auth.redirectUri,
    scope: "openid profile email roles",
    automaticSilentRenew: true,
  };

  return (
    <OidcAuthProvider {...oidcConfig}>
      <OidcAuthContextProvider postLogoutRedirectUri={config.auth.redirectUri}>{children}</OidcAuthContextProvider>
    </OidcAuthProvider>
  );
}

function ApiKeyAuthContextProvider({ children, apiKey }: { children: ReactNode; apiKey: string }) {
  useLayoutEffect(() => {
    setApiKey(apiKey);
    return clearCredentials;
  }, [apiKey]);

  const value: AuthContextValue = {
    user: { profile: { name: "Local Developer" } },
    isLoading: false,
    isAuthenticated: true,
    login: () => {},
    logout: () => {},
    canLogout: false,
    hasRole: (role) => role === "admin" || role === "auditor",
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

function OidcAuthContextProvider({
  children,
  postLogoutRedirectUri,
}: {
  children: ReactNode;
  postLogoutRedirectUri: string;
}) {
  const auth = useOidcAuth();

  // Update access token when user changes
  useLayoutEffect(() => {
    if (auth.user?.access_token) {
      setAccessToken(auth.user.access_token);
    } else {
      setAccessToken("");
    }
  }, [auth.user?.access_token]);

  const hasRole = (role: string): boolean => {
    // Check realm_access.roles (Keycloak standard claim)
    const realmAccess = auth.user?.profile?.realm_access as { roles?: string[] } | undefined;
    if (realmAccess?.roles && Array.isArray(realmAccess.roles)) {
      return realmAccess.roles.includes(role);
    }

    // Fallback to direct roles claim
    const roles = auth.user?.profile?.roles;
    if (Array.isArray(roles)) {
      return roles.includes(role);
    }

    return false;
  };

  const value: AuthContextValue = {
    user: auth.user,
    isLoading: auth.isLoading,
    isAuthenticated: auth.isAuthenticated,
    login: () => auth.signinRedirect(),
    logout: () => auth.signoutRedirect({ post_logout_redirect_uri: postLogoutRedirectUri }),
    canLogout: true,
    hasRole,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

interface RequireAuthProps {
  roles?: string[];
  children: ReactNode;
}

export function RequireAuth({ roles = ["admin", "auditor"], children }: RequireAuthProps) {
  const auth = useAuth();

  if (auth.isLoading) {
    return (
      <div className="console-shell flex h-screen items-center justify-center">
        <div className="console-panel rounded-2xl p-10 text-center">
          <div className="mb-4 h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent"></div>
          <p className="text-muted-foreground">Loading...</p>
        </div>
      </div>
    );
  }

  if (!auth.isAuthenticated) {
    return (
      <div className="console-shell flex h-screen items-center justify-center">
        <div className="console-panel max-w-md rounded-2xl p-10 text-center">
          <h1 className="console-title mb-4 text-3xl">Authentication Required</h1>
          <p className="mb-6 text-muted-foreground">Please sign in to access the developer console.</p>
          <button
            onClick={auth.login}
            className="rounded-lg bg-primary px-6 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            Sign In
          </button>
        </div>
      </div>
    );
  }

  const hasRequiredRole = roles.some((role) => auth.hasRole(role));
  if (!hasRequiredRole) {
    return (
      <div className="console-shell flex h-screen items-center justify-center">
        <div className="console-panel max-w-md rounded-2xl p-10 text-center">
          <h1 className="console-title mb-4 text-3xl">Access Denied</h1>
          <p className="mb-6 text-muted-foreground">You need admin or auditor role to access this console.</p>
          <button
            onClick={auth.logout}
            className="rounded-lg bg-destructive px-6 py-2 text-destructive-foreground hover:bg-destructive/90"
          >
            Sign Out
          </button>
        </div>
      </div>
    );
  }

  return <>{children}</>;
}

// Made with Bob
