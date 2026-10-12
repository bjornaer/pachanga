import { createInstance } from "i18next";
import { initReactI18next } from "react-i18next";
import { pickLocale, resources } from "@pachanga/i18n";

const i18n = createInstance({
  resources,
  lng: pickLocale(navigator.languages),
  fallbackLng: "es-ES",
  interpolation: { escapeValue: false },
});
void i18n.use(initReactI18next).init();

export default i18n;
