export type Access = {role: string|null;edition: string;simple: boolean;publicFeatures?: boolean};
export function allowedRoute(meta: {owner?:unknown;professional?:unknown;pro?:unknown;publicFeatures?:unknown}, access: Access): boolean {
  return !!access.role && (!meta.owner || access.role === 'owner') && (!meta.professional || !access.simple) && (!meta.pro || access.edition === 'pro') && (!meta.publicFeatures || !!access.publicFeatures);
}
