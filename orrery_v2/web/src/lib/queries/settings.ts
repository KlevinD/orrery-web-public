import {
  createMutation,
  createQuery,
  useQueryClient,
} from "@tanstack/svelte-query";
import { settingsApi } from "#lib/api/settings";
import {
  mapPageIndexConnectionTestCommand,
  mapPageIndexConnectionTestView,
  mapRuntimeDiscoveryView,
  mapSettingsCommand,
  mapSettingsView,
} from "#lib/mappers/settings";
import type {
  RuntimeDiscoveryResponseDTO,
  SettingsDTO,
  SettingsPageIndexConnectionTestResultDTO,
} from "#lib/types/api";
import type {
  SettingsPageIndexKnowledgeViewModel,
  SettingsViewModel,
} from "#lib/types/view-models";

export const settingsQueryKeys = {
  detail: ["settings"] as const,
  runtimeDiscovery: ["settings", "runtime-discovery"] as const,
};

export function useSettingsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: settingsQueryKeys.detail,
    queryFn: async () =>
      mapSettingsView((await settingsApi.getSettings()) as SettingsDTO),
    enabled: enabled(),
  }));
}

export function useSettingsUpdateMutation() {
  const client = useQueryClient();

  return createMutation(() => ({
    mutationFn: (body: SettingsViewModel) =>
      settingsApi.updateSettings(mapSettingsCommand(body)),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: settingsQueryKeys.detail }),
  }));
}

export function useRuntimeDiscoveryQuery() {
  return createQuery(() => ({
    queryKey: settingsQueryKeys.runtimeDiscovery,
    enabled: false,
    queryFn: async () =>
      mapRuntimeDiscoveryView(
        (await settingsApi.discoverRuntimeCandidates()) as RuntimeDiscoveryResponseDTO,
      ),
  }));
}

export function usePageIndexConnectionTestMutation() {
  return createMutation(() => ({
    mutationFn: async (body: SettingsPageIndexKnowledgeViewModel) =>
      mapPageIndexConnectionTestView(
        (await settingsApi.testPageIndexConnection(
          mapPageIndexConnectionTestCommand(body),
        )) as SettingsPageIndexConnectionTestResultDTO,
      ),
  }));
}
