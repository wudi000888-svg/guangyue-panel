export type Access = {role: string|null;edition: string;simple: boolean};
export function allowedRoute(meta: {owner?:unknown;professional?:unknown;pro?:unknown}, access: Access): boolean {
  return !!access.role && (!meta.owner || access.role === 'owner') && (!meta.professional || !access.simple) && (!meta.pro || access.edition === 'pro');
}
