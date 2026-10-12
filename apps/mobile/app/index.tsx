import { Redirect } from "expo-router";
import { ActivityIndicator, View } from "react-native";
import { useSession } from "../lib/session";

export default function Index() {
  const { me, loading } = useSession();
  if (loading)
    return (
      <View style={{ flex: 1, justifyContent: "center" }}>
        <ActivityIndicator />
      </View>
    );
  if (!me) return <Redirect href="/login" />;
  if (!me.email_verified) return <Redirect href="/verify" />;
  return <Redirect href="/home" />;
}
