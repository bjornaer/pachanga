import { router } from "expo-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ScrollView, Text } from "react-native";
import { useSession } from "../lib/session";
import { Button, ErrorText, Field, s } from "../lib/ui";

export default function Verify() {
  const { t } = useTranslation();
  const { api, me, reloadMe, signOut } = useSession();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function submit() {
    setError(null);
    const { error, response } = await api.POST("/v1/auth/verify-email", { body: { code } });
    if (!response.ok) return setError(error?.code ?? "generic");
    await reloadMe();
    router.replace("/home");
  }

  async function resend() {
    await api.POST("/v1/auth/verify-email/resend");
    setNotice(t("auth.codeOnItsWay"));
  }

  return (
    <ScrollView contentContainerStyle={s.screen}>
      <Text style={s.title}>{t("auth.verifyTitle")}</Text>
      <Text>{t("auth.verifyHint", { email: me?.email ?? "" })}</Text>
      <Field label={t("auth.code")} value={code} onChangeText={setCode} keyboardType="number-pad" maxLength={6} autoComplete="one-time-code" />
      <ErrorText code={error} />
      {notice && <Text>{notice}</Text>}
      <Button title={t("auth.verify")} onPress={submit} disabled={code.length !== 6} />
      <Button title={t("auth.resend")} onPress={resend} />
      <Button title={t("auth.logout")} onPress={() => signOut().then(() => router.replace("/login"))} />
    </ScrollView>
  );
}
