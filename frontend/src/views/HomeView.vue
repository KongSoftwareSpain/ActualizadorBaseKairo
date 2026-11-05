<script setup lang="ts">
import { ref } from "vue";
import PasswordInput from "@/components/PasswordInput.vue";

const name = ref("");
const password = ref("");
</script>

<template>
  <UContainer class="py-6">
    <UPageCard
      title="Hola"
      description="Página de ejemplo. Reemplázala con tu vista."
    >
      <UInput v-model="name" placeholder="Nombre" />
      <PasswordInput v-model="password" placeholder="Contraseña" />
    </UPageCard>
  </UContainer>
</template>
