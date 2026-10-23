export interface EmailProof { email: string; token: string; expires: number }
const key = 'guangyue-email-registration';
let memoryProof: EmailProof | null = null;
let memoryOnly = false;
// Registration proof is short lived and scoped to this tab. Link tokens are never saved.
export function saveEmailProof(email: string, token: string): void {
  memoryProof = { email, token, expires: Date.now() + 600_000 };
  try { sessionStorage.setItem(key, JSON.stringify(memoryProof)); memoryOnly = false; } catch { memoryOnly = true; }
}
export function clearEmailProof(): void { memoryProof = null; memoryOnly = false; try { sessionStorage.removeItem(key); } catch { /* Storage may be disabled. */ } }
export function readEmailProof(): EmailProof | null {
  try {
    let raw: string | null;
    try { raw = memoryOnly ? JSON.stringify(memoryProof) : sessionStorage.getItem(key); } catch { raw = memoryProof ? JSON.stringify(memoryProof) : null; }
    const value = JSON.parse(raw || 'null');
    if (!value || typeof value.email !== 'string' || !value.email.trim() || typeof value.token !== 'string' || value.token.length < 32 || !Number.isFinite(value.expires) || value.expires <= Date.now() || value.expires > Date.now() + 600_000) {
      clearEmailProof(); return null;
    }
    return value;
  } catch { clearEmailProof(); return null; }
}
// Deliveries in these states can still change without user action; the mail
// settings page polls only while one exists.
const settlingDeliveryStates = new Set(['pending', 'sending', 'retry']);
export function hasSettlingDelivery(items: readonly { state: string }[]): boolean {
  return items.some(item => settlingDeliveryStates.has(item.state));
}
