// The theme editor exports app.config.ts for Nuxt, where defineAppConfig is a
// global. This provides it so the export can be pasted unchanged. Import this
// module before app.config.ts.

declare global {
  function defineAppConfig<T>(config: T): T;
}

globalThis.defineAppConfig = (config) => config;

export {};
