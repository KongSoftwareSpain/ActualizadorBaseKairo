<template>
  <footer
    class="flex shrink-0 items-center gap-3 border-t border-default bg-default px-4 py-3"
  >
    <div class="flex min-w-0 flex-1 items-center gap-2 text-sm text-muted">
      <slot />
    </div>

    <slot name="actions" />
  </footer>
</template>
