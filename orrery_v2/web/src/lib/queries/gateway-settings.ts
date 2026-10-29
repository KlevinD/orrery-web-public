import { createMutation, createQuery, useQueryClient } from '@tanstack/svelte-query'
import { gatewaySettingsApi } from '#lib/api/gateway-settings'
import {
  mapGatewaySettingsCommand,
  mapGatewaySettingsView,
  mapRawGatewaySettingsCommand
} from '#lib/mappers/gateway-settings'
import type { GatewaySettingsDTO } from '#lib/types/api'
import type { GatewaySettingsViewModel } from '#lib/types/view-models'

export const gatewaySettingsQueryKeys = {
  detail: ['gateway-settings'] as const,
  accounts: ['gateway-settings', 'accounts'] as const
}

export function useGatewaySettingsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: gatewaySettingsQueryKeys.detail,
    enabled: enabled(),
    queryFn: async () => mapGatewaySettingsView((await gatewaySettingsApi.getSettings()) as GatewaySettingsDTO)
  }))
}

export function useGatewayAccountsQuery(enabled: () => boolean = () => true) {
  return createQuery(() => ({
    queryKey: gatewaySettingsQueryKeys.accounts,
    enabled: enabled(),
    queryFn: async () => (await gatewaySettingsApi.getAccounts()).accounts
  }))
}

export function useGatewaySettingsUpdateMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (body: GatewaySettingsViewModel) =>
      gatewaySettingsApi.updateSettings(mapGatewaySettingsCommand(body)),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: gatewaySettingsQueryKeys.detail })
      await client.invalidateQueries({ queryKey: gatewaySettingsQueryKeys.accounts })
    }
  }))
}

export function useGatewaySettingsRawUpdateMutation() {
  const client = useQueryClient()

  return createMutation(() => ({
    mutationFn: (body: GatewaySettingsViewModel) =>
      gatewaySettingsApi.updateRawSettings(mapRawGatewaySettingsCommand(body)),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: gatewaySettingsQueryKeys.detail })
      await client.invalidateQueries({ queryKey: gatewaySettingsQueryKeys.accounts })
    }
  }))
}
