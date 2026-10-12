import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { createApi, type Schemas } from "@pachanga/api-client";
import "./i18n";

// Admin tokens stay in memory: reload = log in again (TOTP each time).
let access: string | null = null;
const api = createApi(import.meta.env.VITE_API_URL ?? "", () => access);

export default function App() {
  const { t } = useTranslation();
  const [me, setMe] = useState<Schemas["Me"] | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function login(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const body = { email: String(f.get("email")), password: String(f.get("password")), totp: String(f.get("totp")) };
    const res = await api.POST("/admin/v1/auth/login", { body });
    if (!res.data) return setError(res.error?.code ?? "generic");
    access = res.data.access_token;
    const m = await api.GET("/admin/v1/me");
    setMe(m.data ?? null);
  }

  if (me)
    return (
      <main>
        <h1>{t("app.name")} admin</h1>
        <p>{t("home.hello", { name: me.display_name })}</p>
        <button onClick={() => { access = null; setMe(null); }}>{t("auth.logout")}</button>
      </main>
    );

  return (
    <main>
      <h1>{t("app.name")} admin</h1>
      <form onSubmit={login} style={{ display: "grid", gap: 8, maxWidth: 320 }}>
        <label>{t("auth.email")}<input name="email" type="email" required autoComplete="username" /></label>
        <label>{t("auth.password")}<input name="password" type="password" required autoComplete="current-password" /></label>
        <label>{t("auth.totp")}<input name="totp" inputMode="numeric" pattern="[0-9]{6}" required autoComplete="one-time-code" /></label>
        {error && <p role="alert">{t(`errors.${error}`, { defaultValue: t("errors.generic") })}</p>}
        <button type="submit">{t("auth.login")}</button>
      </form>
    </main>
  );
}
