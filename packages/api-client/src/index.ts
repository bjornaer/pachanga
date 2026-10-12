import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type Problem = Schemas["Problem"];

export function createApi(baseUrl: string, getToken?: () => string | null | undefined) {
  const client = createClient<paths>({ baseUrl });
  client.use({
    onRequest({ request }) {
      const t = getToken?.();
      if (t) request.headers.set("Authorization", `Bearer ${t}`);
      return request;
    },
  });
  return client;
}

export type Api = ReturnType<typeof createApi>;
