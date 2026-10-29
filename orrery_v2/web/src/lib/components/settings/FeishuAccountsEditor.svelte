<script lang="ts">
  import { Link2, Pencil, Plus, Save, Trash2, X } from 'lucide-svelte'
  import Button from '#lib/components/ui/Button.svelte'
  import Input from '#lib/components/ui/Input.svelte'
  import Select from '#lib/components/ui/Select.svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import type { GatewayFeishuAccountViewModel } from '#lib/types/view-models'

  interface Props {
    accounts?: Record<string, GatewayFeishuAccountViewModel>
    busy?: boolean
    onaccountschange?: (accounts: Record<string, GatewayFeishuAccountViewModel>) => void
    onregister?: (accountId: string) => void
  }

  let { accounts = {}, busy = false, onaccountschange, onregister }: Props = $props()
  let t = $derived(createTranslator($locale))
  let editorOpen = $state(false)
  let editingAlias = $state('')
  let accountAlias = $state('')
  let account = $state<GatewayFeishuAccountViewModel>(emptyAccount())
  let replacementSecret = $state('')
  let sortedAccounts = $derived(Object.entries(accounts).sort(([left], [right]) => left.localeCompare(right)))
  let aliasValid = $derived(/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(accountAlias.trim()))
  let effectiveSecret = $derived(replacementSecret.trim() || (editingAlias ? accounts[editingAlias]?.app_secret ?? '' : ''))
  let canSave = $derived(aliasValid && (!account.enabled || (Boolean(account.app_id?.trim()) && Boolean(effectiveSecret))))

  function emptyAccount(): GatewayFeishuAccountViewModel {
    return {
      enabled: true,
      app_id: '',
      app_secret: '',
      encrypt_key: '',
      verify_token: '',
      default_session_label: 'general',
      connection_mode: 'stream',
      domain_name: 'feishu'
    }
  }

  function openCreate() {
    editingAlias = ''
    accountAlias = ''
    account = emptyAccount()
    replacementSecret = ''
    editorOpen = true
  }

  function openEdit(alias: string, current: GatewayFeishuAccountViewModel) {
    editingAlias = alias
    accountAlias = alias
    account = { ...current }
    replacementSecret = ''
    editorOpen = true
  }

  function closeEditor() {
    editorOpen = false
    editingAlias = ''
    accountAlias = ''
    account = emptyAccount()
    replacementSecret = ''
  }

  function saveAccount() {
    const alias = accountAlias.trim()
    if (!canSave) return
    const existing = editingAlias ? accounts[editingAlias] : undefined
    const next = cloneAccounts(accounts)
    next[alias] = {
      ...existing,
      ...account,
      app_id: account.app_id?.trim() ?? '',
      app_secret: replacementSecret.trim() || existing?.app_secret || '',
      default_session_label: account.default_session_label?.trim() || 'general',
      connection_mode: account.connection_mode?.trim() || 'stream',
      domain_name: account.domain_name?.trim() || 'feishu'
    }
    onaccountschange?.(next)
    closeEditor()
  }

  function deleteAccount(alias: string) {
    const next = cloneAccounts(accounts)
    delete next[alias]
    onaccountschange?.(next)
    if (editingAlias === alias) closeEditor()
  }

  function registerAccount() {
    const alias = accountAlias.trim()
    if (!aliasValid) return
    onregister?.(alias)
  }

  function cloneAccounts(source: Record<string, GatewayFeishuAccountViewModel>) {
    return Object.fromEntries(Object.entries(source).map(([alias, value]) => [alias, { ...value }]))
  }
</script>

<section class="grid gap-3" aria-label={t('settings.feishuAccounts.title')}>
  <div class="flex items-center justify-between gap-3">
    <h3 class="text-xs font-semibold text-foreground">{t('settings.feishuAccounts.title')}</h3>
    <Button variant="outline" size="sm" disabled={busy} onclick={openCreate}>
      <Plus class="h-4 w-4" />{t('settings.feishuAccounts.add')}
    </Button>
  </div>

  {#if sortedAccounts.length > 0}
    <div class="divide-y divide-border border-y border-border">
      {#each sortedAccounts as [alias, value] (alias)}
        <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3">
          <div class="min-w-0">
            <p class="truncate text-sm font-medium text-foreground" title={alias}>{alias}</p>
            <p class="mt-0.5 truncate text-xs text-muted-foreground">{value.app_id || t('settings.feishuAccounts.noAppId')}</p>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            <span class="mr-1 text-xs {value.enabled ? 'text-emerald-700' : 'text-muted-foreground'}">
              {value.enabled ? t('settings.enabled') : t('settings.disabled')}
            </span>
            <Button
              variant="ghost"
              size="icon"
              ariaLabel={`${t('settings.feishuAccounts.register')} ${alias}`}
              title={t('settings.feishuAccounts.register')}
              disabled={busy}
              onclick={() => onregister?.(alias)}
            >
              <Link2 class="h-4 w-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              ariaLabel={`${t('settings.feishuAccounts.edit')} ${alias}`}
              title={t('settings.feishuAccounts.edit')}
              disabled={busy}
              onclick={() => openEdit(alias, value)}
            >
              <Pencil class="h-4 w-4" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              ariaLabel={`${t('settings.feishuAccounts.delete')} ${alias}`}
              title={t('settings.feishuAccounts.delete')}
              disabled={busy}
              onclick={() => deleteAccount(alias)}
            >
              <Trash2 class="h-4 w-4 text-destructive" />
            </Button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if editorOpen}
    <div class="grid gap-3 border-t border-border pt-3">
      <div class="flex items-center justify-between gap-3">
        <p class="text-xs font-semibold text-foreground">
          {editingAlias ? t('settings.feishuAccounts.editTitle') : t('settings.feishuAccounts.addTitle')}
        </p>
        <Button variant="ghost" size="icon" ariaLabel={t('settings.cancel')} onclick={closeEditor}>
          <X class="h-4 w-4" />
        </Button>
      </div>

      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.feishuAccounts.alias')}</span>
        <Input bind:value={accountAlias} disabled={Boolean(editingAlias)} />
      </label>

      <label class="flex items-center justify-between gap-4 border-y border-border py-2.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.enabled')}</span>
        <input class="h-4 w-4 accent-brand" type="checkbox" bind:checked={account.enabled} />
      </label>

      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">{t('settings.feishuAccounts.appId')}</span>
        <Input bind:value={account.app_id} />
      </label>

      <label class="space-y-1.5">
        <span class="text-xs font-medium text-muted-foreground">
          {editingAlias ? t('settings.feishuAccounts.replacementSecret') : t('settings.feishuAccounts.appSecret')}
        </span>
        <Input type="password" autocomplete="new-password" bind:value={replacementSecret} />
      </label>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label class="space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.feishuAccounts.domain')}</span>
          <Select bind:value={account.domain_name} options={[{ label: 'Feishu', value: 'feishu' }, { label: 'Lark', value: 'lark' }]} />
        </label>
        <label class="space-y-1.5">
          <span class="text-xs font-medium text-muted-foreground">{t('settings.feishuAccounts.connectionMode')}</span>
          <Select bind:value={account.connection_mode} options={[{ label: 'Stream', value: 'stream' }, { label: 'Outbound', value: 'outbound' }]} />
        </label>
      </div>

      <div class="flex flex-wrap justify-end gap-2 pt-1">
        <Button variant="outline" disabled={!aliasValid || busy} onclick={registerAccount}>
          <Link2 class="h-4 w-4" />{t('settings.feishuAccounts.register')}
        </Button>
        <Button disabled={!canSave || busy} onclick={saveAccount}>
          <Save class="h-4 w-4" />{t('settings.feishuAccounts.save')}
        </Button>
      </div>
    </div>
  {/if}
</section>
