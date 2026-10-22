import type { QRCodeToDataURLOptions } from 'qrcode';
// Loaded on first use so the QR encoder stays out of the entry chunk.
export async function qrDataURL(text: string, options: QRCodeToDataURLOptions): Promise<string> {
  const { default: QRCode } = await import('qrcode');
  return QRCode.toDataURL(text, options);
}
