import * as Location from "expo-location";
import { Link, router } from "expo-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Pressable, ScrollView, Switch, Text } from "react-native";
import type { Schemas } from "@pachanga/api-client";
import { TERMS_VERSION } from "@pachanga/i18n";
import i18n from "../lib/i18n";
import { useSession } from "../lib/session";
import { Button, ErrorText, Field, s } from "../lib/ui";

const GENDERS: Schemas["Gender"][] = ["male", "female", "non_binary", "prefer_not_to_say"];

export default function Signup() {
  const { t } = useTranslation();
  const { api, signIn } = useSession();
  const [f, setF] = useState({ email: "", password: "", display_name: "", birth_date: "" });
  const [gender, setGender] = useState<Schemas["Gender"] | null>(null);
  const [home, setHome] = useState<Schemas["GeoPoint"] | null>(null);
  const [adult, setAdult] = useState(false);
  const [terms, setTerms] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof f) => (v: string) => setF({ ...f, [k]: v });

  // ponytail: device location only; map pin picker arrives with M1 maps.
  async function locate() {
    const { status } = await Location.requestForegroundPermissionsAsync();
    if (status !== "granted") return setError("location_required");
    const pos = await Location.getCurrentPositionAsync({ accuracy: Location.Accuracy.Balanced });
    setHome({ lat: pos.coords.latitude, lng: pos.coords.longitude });
  }

  async function submit() {
    if (!home) return setError("location_required");
    if (!gender) return setError("invalid_request");
    setBusy(true);
    setError(null);
    const { data, error } = await api.POST("/v1/auth/signup", {
      body: {
        ...f,
        gender,
        home_location: home,
        adult_attested: adult,
        terms_version: TERMS_VERSION,
        locale: i18n.language === "en" ? "en" : "es-ES",
      },
    });
    setBusy(false);
    if (!data) return setError(error?.code ?? "generic");
    await signIn(data);
    router.replace("/verify");
  }

  return (
    <ScrollView contentContainerStyle={s.screen}>
      <Text style={s.title}>{t("auth.signup")}</Text>
      <Field label={t("auth.email")} value={f.email} onChangeText={set("email")} keyboardType="email-address" />
      <Field label={t("auth.password")} value={f.password} onChangeText={set("password")} secureTextEntry />
      <Field label={t("auth.displayName")} value={f.display_name} onChangeText={set("display_name")} autoCapitalize="words" />
      <Field label={t("auth.birthDate")} value={f.birth_date} onChangeText={set("birth_date")} placeholder="1995-06-21" />
      <Text style={s.label}>{t("auth.gender")}</Text>
      {GENDERS.map((g) => (
        <Pressable key={g} accessibilityRole="radio" accessibilityState={{ checked: gender === g }} onPress={() => setGender(g)} style={s.row}>
          <Text>{gender === g ? "◉" : "○"} {t(`auth.genders.${g}`)}</Text>
        </Pressable>
      ))}
      <Text style={s.label}>{t("auth.homeArea")}</Text>
      {home ? <Text>{t("auth.homeAreaSet")}</Text> : <Button title={t("auth.useMyLocation")} onPress={locate} />}
      <Pressable style={s.row} onPress={() => setAdult(!adult)}>
        <Switch value={adult} onValueChange={setAdult} accessibilityLabel={t("auth.adultAttestation")} />
        <Text style={{ flex: 1 }}>{t("auth.adultAttestation")}</Text>
      </Pressable>
      <Pressable style={s.row} onPress={() => setTerms(!terms)}>
        <Switch value={terms} onValueChange={setTerms} accessibilityLabel={t("auth.acceptTerms")} />
        <Text style={{ flex: 1 }}>{t("auth.acceptTerms")}</Text>
      </Pressable>
      <ErrorText code={error} />
      <Button title={t("auth.signup")} onPress={submit} disabled={busy || !adult || !terms} />
      <Link href="/login" style={s.link}>
        {t("auth.haveAccount")}
      </Link>
    </ScrollView>
  );
}
