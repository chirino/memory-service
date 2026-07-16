export type AppAuthConfig =
  | {
      mode: "oidc";
      authority: string;
      clientId: string;
      redirectUri: string;
    }
  | {
      mode: "api-key";
      apiKey: string;
      clientId: string;
    };

export interface AppConfig {
  apiUrl: string;
  auth: AppAuthConfig;
}

let cachedConfig: AppConfig | null = null;

export async function getConfig(): Promise<AppConfig> {
  if (cachedConfig) {
    return cachedConfig;
  }

  // Use BASE_URL to account for base path (e.g., /developer/)
  const configPath = `${import.meta.env.BASE_URL}config.json`;
  const response = await fetch(configPath);
  if (!response.ok) {
    throw new Error(`Failed to load configuration from ${configPath}`);
  }

  cachedConfig = await response.json();
  return cachedConfig!;
}

// Made with Bob
