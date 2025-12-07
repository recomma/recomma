export function isAuthErrorStatus(status?: number): boolean {
  return status === 401 || status === 403;
}

export function redirectToLogin(): void {
  if (typeof window === 'undefined') {
    return;
  }
  window.location.href = '/';
}
