export type Access = {role: string|null;edition: string;publicFeatures?: boolean};
export function allowedRoute(meta: {owner?:unknown;pro?:unknown;publicFeatures?:unknown}, access: Access): boolean {
  return !!access.role && (!meta.owner || access.role === 'owner') && (!meta.pro || access.edition === 'pro') && (!meta.publicFeatures || !!access.publicFeatures);
}
