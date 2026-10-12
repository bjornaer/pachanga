import { Link, router } from "expo-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ScrollView, Text } from "react-native";
import { useSession } from "../lib/session";
import { Button, ErrorText, Field, s } from "../lib/ui";

export default function Login() {
  const { t } = useTranslation();
  const { api, signIn } = useSession();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setError(null);
    const { data, error } = await api.POST("/v1/auth/login", { body: { email, password } });
    setBusy(false);
    if (!data) return setError(error?.code ?? "generic");
    await signIn(data);
    router.replace("/");
  }

  return (
    <ScrollView contentContainerStyle={s.screen}>
      <Text style={s.title}>{t("app.name")}</Text>
      <Field label={t("auth.email")} value={email} onChangeText={setEmail} keyboardType="email-address" autoComplete="email" />
      <Field label={t("auth.password")} value={password} onChangeText={setPassword} secureTextEntry autoComplete="password" />
      <ErrorText code={error} />
      <Button title={t("auth.login")} onPress={submit} disabled={busy} />
      <Link href="/signup" style={s.link}>
        {t("auth.noAccount")}
      </Link>
    </ScrollView>
  );
}
