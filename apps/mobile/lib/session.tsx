import * as SecureStore from "expo-secure-store";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { createApi, type Schemas } from "@pachanga/api-client";

const API_URL = process.env.EXPO_PUBLIC_API_URL ?? "http://localhost:8080";
const REFRESH_KEY = "refresh_token";

// Access token lives in memory only; the refresh token in the device keychain.
let access: string | null = null;
export const api = createApi(API_URL, () => access);

// Rotates the stored refresh token; false when the session is gone.
async function refresh() {
  const rt = await SecureStore.getItemAsync(REFRESH_KEY);
  if (!rt) return false;
  const { data } = await api.POST("/v1/auth/refresh", { body: { refresh_token: rt } });
  if (!data) {
    await SecureStore.deleteItemAsync(REFRESH_KEY);
    return false;
  }
  access = data.access_token;
  await SecureStore.setItemAsync(REFRESH_KEY, data.refresh_token);
  return true;
}

async function fetchMe() {
  let res = await api.GET("/v1/me");
  if (res.response.status === 401 && (await refresh())) res = await api.GET("/v1/me");
  return res.data ?? null;
}

type Session = {
  api: typeof api;
  me: Schemas["Me"] | null;
  loading: boolean;
  signIn: (t: Schemas["TokenPair"]) => Promise<void>;
  signOut: () => Promise<void>;
  reloadMe: () => Promise<void>;
};

const Ctx = createContext<Session | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Schemas["Me"] | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      if (await refresh()) setMe(await fetchMe());
      setLoading(false);
    })();
  }, []);

  const reloadMe = async () => setMe(await fetchMe());

  const signIn = async (t: Schemas["TokenPair"]) => {
    access = t.access_token;
    await SecureStore.setItemAsync(REFRESH_KEY, t.refresh_token);
    await reloadMe();
  };

  const signOut = async () => {
    const rt = await SecureStore.getItemAsync(REFRESH_KEY);
    if (rt) await api.POST("/v1/auth/logout", { body: { refresh_token: rt } });
    await SecureStore.deleteItemAsync(REFRESH_KEY);
    access = null;
    setMe(null);
  };

  return <Ctx.Provider value={{ api, me, loading, signIn, signOut, reloadMe }}>{children}</Ctx.Provider>;
}

export function useSession() {
  const s = useContext(Ctx);
  if (!s) throw new Error("useSession outside SessionProvider");
  return s;
}
