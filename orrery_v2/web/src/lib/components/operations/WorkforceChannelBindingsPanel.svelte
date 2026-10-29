<script lang="ts">
  import { Link2, Trash2 } from 'lucide-svelte'
  import Badge from '#lib/components/ui/Badge.svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { WorkforceChannelAccountViewModel } from '#lib/types/view-models'

  interface Props {
    targetAgentProfileId: string
    accounts?: WorkforceChannelAccountViewModel[]
    loading?: boolean
    error?: string
    canManage?: boolean
    busyAccountId?: string
    onassign?: (account: WorkforceChannelAccountViewModel) => void
    ontoggle?: (account: WorkforceChannelAccountViewModel, inboundEnabled: boolean) => void
    ondelete?: (account: WorkforceChannelAccountViewModel) => void
  }

  let {
    targetAgentProfileId,
    accounts = [],
    loading = false,
    error = '',
    canManage = false,
    busyAccountId = '',
    onassign,
    ontoggle,
    ondelete
  }: Props = $props()
  let t = $derived(createTranslator($locale))
  let selectedAccountId = $state('')
  const assignableAccounts = $derived(accounts.filter((account) => !account.ownedByTarget))
  const accountOptions = $derived(assignableAccounts.map((account) => ({ label: account.accountId, value: account.accountId })))
  const selectedAccount = $derived(assignableAccounts.find((account) => account.accountId === selectedAccountId))

  $effect(() => {
    if (!assignableAccounts.some((account) => account.accountId === selectedAccountId)) {
      selectedAccountId = assignableAccounts[0]?.accountId ?? ''
    }
  })

  function assignSelected() {
    if (!canManage || !selectedAccount) return
    if (selectedAccount.ownerAgentProfileId && selectedAccount.ownerAgentProfileId !== targetAgentProfileId) {
      const previousOwner = selectedAccount.ownerDisplayName || selectedAccount.ownerAgentProfileId
      if (!window.confirm(t('people.feishuMoveConfirm', { account: selectedAccount.accountId, owner: previousOwner }))) return
    }
    onassign?.(selectedAccount)
  }

  function statusLabel(status: string) {
    switch (status.trim().toLowerCase()) {
      case 'connected': return t('people.feishuStatusConnected')
      case 'connecting': return t('people.feishuStatusConnecting')
      case 'degraded': return t('people.feishuStatusDegraded')
      default: return t('people.feishuStatusUnavailable')
    }
  }
</script>

<section aria-label={t('people.feishuConnections')} class="min-w-0">
  {#if loading}
    <p class="text-sm text-muted-foreground">{t('state.loadingWorkforce')}</p>
  {:else if error}
    <p class="text-sm text-destructive">{error}</p>
  {:else}
    {#if assignableAccounts.length > 0}
      <div class="flex min-w-0 flex-col gap-2 border-b border-border pb-4 sm:flex-row sm:items-end">
        <label class="min-w-0 flex-1 space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('people.feishuAccount')}</span>
          <Select bind:value={selectedAccountId} options={accountOptions} disabled={!canManage || Boolean(busyAccountId)} />
        </label>
        <Button size="sm" disabled={!canManage || !selectedAccount || Boolean(busyAccountId)} onclick={assignSelected}>
          <Link2 class="h-4 w-4" />{t('people.feishuAssignAccount')}
        </Button>
      </div>
    {/if}

    <div class="divide-y divide-border">
      {#each accounts as account (account.key)}
        <div data-channel-account-row class="grid min-w-0 gap-3 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(120px,0.65fr)_auto] sm:items-center">
          <div class="min-w-0">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <p class="min-w-0 break-all text-sm font-medium text-foreground">{account.accountId}</p>
              <Badge tone={account.statusTone}>{statusLabel(account.status)}</Badge>
              {#if !account.enabled}<Badge tone="neutral">{t('kpi.disabled')}</Badge>{/if}
            </div>
            <p class="mt-1 truncate text-xs text-muted-foreground">
              {t('people.feishuOwner')}: {account.ownerDisplayName || account.ownerAgentProfileId || t('people.feishuUnassigned')}
            </p>
          </div>

          <label class="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              class="h-4 w-4 shrink-0 accent-brand"
              checked={account.inboundEnabled}
              disabled={!canManage || !account.ownedByTarget || busyAccountId === account.accountId}
              aria-label={`${t('people.feishuInbound')} ${account.accountId}`}
              onchange={(event) => ontoggle?.(account, event.currentTarget.checked)}
            />
            <span class="truncate">{t('people.feishuInbound')}</span>
          </label>

          <div class="flex min-h-9 shrink-0 items-center justify-end">
            {#if account.ownedByTarget}
              <Button
                variant="ghost"
                size="icon"
                disabled={!canManage || busyAccountId === account.accountId}
                ariaLabel={`${t('people.feishuUnassign')} ${account.accountId}`}
                title={t('people.feishuUnassign')}
                onclick={() => ondelete?.(account)}
              >
                <Trash2 class="h-4 w-4 text-destructive" />
              </Button>
            {/if}
          </div>
        </div>
      {:else}
        <p class="py-5 text-sm text-muted-foreground">{t('people.feishuNoAccounts')}</p>
      {/each}
    </div>
  {/if}
</section>
