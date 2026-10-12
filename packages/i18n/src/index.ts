import en from "./en.json";
import es from "./es-ES.json";

export const defaultLocale = "es-ES";
export const resources = { "es-ES": { translation: es }, en: { translation: en } } as const;
export const TERMS_VERSION = "2026-10";

// Picks a supported locale from BCP-47 tags, falling back to es-ES.
export function pickLocale(tags: readonly string[]): "es-ES" | "en" {
  for (const t of tags) {
    if (t.startsWith("es")) return "es-ES";
    if (t.startsWith("en")) return "en";
  }
  return defaultLocale;
}
