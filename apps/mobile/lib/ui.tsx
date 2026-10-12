import { Pressable, StyleSheet, Text, TextInput, type TextInputProps } from "react-native";
import { useTranslation } from "react-i18next";

export function Field(props: TextInputProps & { label: string }) {
  return (
    <>
      <Text style={s.label}>{props.label}</Text>
      <TextInput accessibilityLabel={props.label} style={s.input} autoCapitalize="none" {...props} />
    </>
  );
}

export function Button({ title, onPress, disabled }: { title: string; onPress: () => void; disabled?: boolean }) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={title}
      disabled={disabled}
      onPress={onPress}
      style={[s.button, disabled && { opacity: 0.5 }]}
    >
      <Text style={s.buttonText}>{title}</Text>
    </Pressable>
  );
}

// Maps an API problem `code` to a translated message.
export function ErrorText({ code }: { code: string | null }) {
  const { t } = useTranslation();
  if (!code) return null;
  return (
    <Text accessibilityRole="alert" style={s.error}>
      {t(`errors.${code}`, { defaultValue: t("errors.generic") })}
    </Text>
  );
}

export const s = StyleSheet.create({
  screen: { flexGrow: 1, padding: 24, gap: 8, justifyContent: "center" },
  title: { fontSize: 28, fontWeight: "700", marginBottom: 16 },
  label: { fontSize: 14, marginTop: 8 },
  input: { borderWidth: 1, borderColor: "#999", borderRadius: 8, padding: 12, minHeight: 44, fontSize: 16 },
  button: { backgroundColor: "#0a7d3b", borderRadius: 8, minHeight: 48, alignItems: "center", justifyContent: "center", marginTop: 12 },
  buttonText: { color: "#fff", fontSize: 16, fontWeight: "600" },
  link: { color: "#0a7d3b", minHeight: 44, textAlignVertical: "center", paddingVertical: 12 },
  error: { color: "#b00020", marginTop: 8 },
  row: { flexDirection: "row", alignItems: "center", gap: 8, minHeight: 44 },
});
