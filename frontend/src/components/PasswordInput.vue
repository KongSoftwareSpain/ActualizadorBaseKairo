<script setup lang="ts">
import { computed, ref } from "vue";

defineOptions({ inheritAttrs: false });

const password = defineModel<string>({ required: true });

// Turns off the eye button without disabling the field.
const props = defineProps<{ isRevealDisabled?: boolean }>();

const isVisible = ref(false);
const isShown = computed(() => isVisible.value && !props.isRevealDisabled);
</script>

<template>
  <UInput
    v-model="password"
    :type="isShown ? 'text' : 'password'"
    leading-icon="i-lucide-lock"
    autocomplete="off"
    :ui="{ trailing: 'pe-1' }"
    v-bind="$attrs"
  >
    <template #trailing>
      <UButton
        v-if="password !== ''"
        color="neutral"
        variant="link"
        size="sm"
        :icon="isShown ? 'i-lucide-eye-off' : 'i-lucide-eye'"
        :aria-label="isShown ? 'Ocultar contraseña' : 'Mostrar contraseña'"
        :aria-pressed="isShown"
        :disabled="$attrs.disabled === true || isRevealDisabled"
        @click="isVisible = !isVisible"
      />
    </template>
  </UInput>
</template>
