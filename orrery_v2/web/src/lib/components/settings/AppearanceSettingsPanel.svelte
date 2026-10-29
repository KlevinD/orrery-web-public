<script lang="ts">
  import { Check, Moon, Sun } from 'lucide-svelte'
  import { createTranslator, locale } from '#lib/i18n'
  import { setThemeBrand, setThemeMode, theme, type ThemeBrand, type ThemeMode } from '#lib/stores/theme.svelte'

  let t = $derived(createTranslator($locale))

  const modeOptions: Array<{ value: ThemeMode; label: 'settings.appearance.light' | 'settings.appearance.dark'; icon: typeof Sun }> = [
    { value: 'light', label: 'settings.appearance.light', icon: Sun },
    { value: 'dark', label: 'settings.appearance.dark', icon: Moon }
  ]
  const brandOptions: Array<{ value: ThemeBrand; label: 'topbar.brand.default' | 'topbar.brand.ocean' | 'topbar.brand.forest' | 'topbar.brand.purple'; color: string }> = [
    { value: 'default', label: 'topbar.brand.default', color: '#5b5bd6' },
    { value: 'ocean', label: 'topbar.brand.ocean', color: '#0891b2' },
    { value: 'forest', label: 'topbar.brand.forest', color: '#15803d' },
    { value: 'purple', label: 'topbar.brand.purple', color: '#7c3aed' }
  ]
</script>

<section class="mx-auto w-full max-w-[760px] py-1">
  <header class="border-b border-border pb-4">
    <h2 class="text-base font-semibold text-foreground">{t('settings.appearance')}</h2>
    <p class="mt-1 text-sm leading-5 text-muted-foreground">{t('settings.appearance.desc')}</p>
  </header>

  <div class="divide-y divide-border">
    <div class="flex flex-col justify-between gap-3 py-5 sm:flex-row sm:items-center">
      <div>
        <h3 class="text-sm font-medium text-foreground">{t('settings.appearance.theme')}</h3>
        <p class="mt-1 text-xs text-muted-foreground">{t('settings.appearance.themeDesc')}</p>
      </div>
      <div class="inline-flex w-fit rounded-md bg-muted p-0.5" role="group" aria-label={t('settings.appearance.theme')}>
        {#each modeOptions as option}
          {@const Icon = option.icon}
          <button
            class="inline-flex h-8 items-center gap-1.5 rounded-[5px] px-3 text-xs transition-colors {theme.mode === option.value
              ? 'bg-card font-medium text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground'}"
            type="button"
            aria-pressed={theme.mode === option.value}
            onclick={() => setThemeMode(option.value)}
          >
            <Icon class="h-3.5 w-3.5" />{t(option.label)}
          </button>
        {/each}
      </div>
    </div>

    <div class="py-5">
      <h3 class="text-sm font-medium text-foreground">{t('settings.appearance.accent')}</h3>
      <p class="mt-1 text-xs leading-5 text-muted-foreground">{t('settings.appearance.accentDesc')}</p>
      <div class="mt-4 flex flex-wrap gap-5">
        {#each brandOptions as option}
          <button
            class="group flex min-w-12 flex-col items-center gap-2 text-xs {theme.brand === option.value ? 'font-medium text-foreground' : 'text-muted-foreground'}"
            type="button"
            aria-label={t(option.label)}
            aria-pressed={theme.brand === option.value}
            onclick={() => setThemeBrand(option.value)}
          >
            <span
              class="grid h-9 w-9 place-items-center rounded-full border-2 border-card ring-offset-2 ring-offset-background transition-transform group-hover:scale-105 {theme.brand === option.value ? 'ring-2 ring-foreground/35' : 'ring-1 ring-border'}"
              style:background-color={option.color}
            >
              {#if theme.brand === option.value}<Check class="h-4 w-4 text-white" />{/if}
            </span>
            <span>{t(option.label)}</span>
          </button>
        {/each}
      </div>
    </div>
  </div>
</section>
