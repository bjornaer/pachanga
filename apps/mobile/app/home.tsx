import { router } from "expo-router";
import { useTranslation } from "react-i18next";
import { ScrollView, Text } from "react-native";
import { useSession } from "../lib/session";
import { Button, s } from "../lib/ui";

export default function Home() {
  const { t } = useTranslation();
  const { me, signOut } = useSession();
  return (
    <ScrollView contentContainerStyle={s.screen}>
      <Text style={s.title}>{t("home.hello", { name: me?.display_name ?? "" })}</Text>
      <Text>{me?.email_verified ? t("home.verified") : t("home.unverified")}</Text>
      <Button title={t("auth.logout")} onPress={() => signOut().then(() => router.replace("/login"))} />
    </ScrollView>
  );
}
