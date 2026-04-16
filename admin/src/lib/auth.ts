const STORAGE_KEY = "proxyness-admin-auth";

export function getCredentials(): string | null {
  return sessionStorage.getItem(STORAGE_KEY);
}

export function setCredentials(user: string, pass: string): void {
  sessionStorage.setItem(STORAGE_KEY, btoa(`${user}:${pass}`));
}

export function clearCredentials(): void {
  sessionStorage.removeItem(STORAGE_KEY);
}

export function authHeaders(): Record<string, string> {
  const creds = getCredentials();
  if (!creds) return {};
  return { Authorization: `Basic ${creds}` };
}
