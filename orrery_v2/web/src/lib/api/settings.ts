import { apiFetch, type ApiFetchOptions } from "./client";

export const settingsApi = {
  getSettings: (options?: ApiFetchOptions) =>
    apiFetch("/api/settings", options),
  discoverRuntimeCandidates: (options?: ApiFetchOptions) =>
    apiFetch("/api/settings/runtime/discovery", options),
  testPageIndexConnection: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch("/api/settings/knowledge/pageindex/test", {
      ...options,
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateSettings: (body: unknown, options?: ApiFetchOptions) =>
    apiFetch("/api/settings", {
      ...options,
      method: "PUT",
      body: JSON.stringify(body),
    }),
};
