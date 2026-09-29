<script lang="ts">
  import { Button, Center, Heading, InternationalizationProvider, LayerProvider, Text, Theme, VisuallyHidden, VStack } from '@astryx-svelte/core';
  import { app } from './lib/state/app.svelte';
  import { yipTheme } from './lib/theme';
  import Shell from './screens/Shell.svelte';
  import SignIn from './screens/SignIn.svelte';
  import Setup from './screens/Setup.svelte';
  import Toasts from './components/Toasts.svelte';
  import Wordmark from './components/Wordmark.svelte';

  // yip's wording where Astryx's generic strings would be vaguer.
  const copy = {
    en: {
      '@astryx.mobileNav.toggle.open': 'Rooms and navigation',
      '@astryx.mobileNav.navigation': 'Rooms and navigation',
    },
  };
</script>

<InternationalizationProvider locale="en" overrides={copy}>
  <Theme theme={yipTheme} mode={app.themeMode}>
    <!-- Hosts Astryx's toast viewport (<Toasts> below), instead of the fallback it builds on <body>. -->
    <LayerProvider>
      {#if app.phase === 'ready'}
        <Shell />
      {:else if app.phase === 'setup'}
        <Setup />
      {:else if app.phase === 'signin'}
        <SignIn />
      {:else if app.phase === 'error'}
        <main class="boot yip-frame" aria-labelledby="boot-title">
          <Center height="100%" padding={6}>
            <VStack gap={3} maxWidth={480} align="start">
              <Wordmark size={28} />
              <Heading level={1} id="boot-title">Can't reach your workspace.</Heading>
              <Text color="secondary">{app.bootError || 'The hub did not respond.'} Drafts you were writing are saved on this device.</Text>
              <Button label="Try again" variant="primary" onclick={() => app.boot()} />
            </VStack>
          </Center>
        </main>
      {:else}
        <main class="boot yip-frame" aria-busy="true" aria-label="Loading your workspace">
          <Center height="100%" padding={6}>
            <VStack gap={3} maxWidth={480} align="start">
              <Wordmark size={28} />
              <Text color="secondary">Opening your workspace…</Text>
            </VStack>
          </Center>
        </main>
      {/if}

      <VisuallyHidden as="div" role="status" aria-live="polite" aria-atomic="true">{app.announcement}</VisuallyHidden>
      <Toasts />
    </LayerProvider>
  </Theme>
</InternationalizationProvider>

<style>
  .boot {
    height: 100%;
  }
</style>
