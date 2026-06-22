import { createContext, useContext, useEffect, type ReactNode } from "react";
import { AuthProvider as OidcAuthProvider, useAuth as useOidcAuth } from "react-oidc-context";
import type { User } from "oidc-client-ts";
import type { AppConfig } from "./config";
import { setAccessToken } from "../api/client";

interface AuthContextValue {
  user: User | null | undefined;
  isLoading: boolean;
  isAuthenticated: boolean;
  login: () => void;
  logout: () => void;
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
  const oidcConfig = {
    authority: config.oidc.authority,
    client_id: config.oidc.clientId,
    redirect_uri: config.oidc.redirectUri,
    post_logout_redirect_uri: config.oidc.redirectUri,
    scope: "openid profile email roles",
    automaticSilentRenew: true,
  };

  return (
    <OidcAuthProvider {...oidcConfig}>
      <AuthContextProvider postLogoutRedirectUri={config.oidc.redirectUri}>{children}</AuthContextProvider>
    </OidcAuthProvider>
  );
}

function AuthContextProvider({
  children,
  postLogoutRedirectUri,
}: {
  children: ReactNode;
  postLogoutRedirectUri: string;
}) {
  const auth = useOidcAuth();

  // Update access token when user changes
  useEffect(() => {
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
          <p className="mb-6 text-muted-foreground">
            You need admin or auditor role to access this console.
          </p>
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
