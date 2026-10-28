<script setup lang="ts">
import { computed } from "vue";
import { apiPath } from "../server";
const props = defineProps<{ path: string; filename?: string }>();
const href = computed(() => apiPath(props.path));
</script>

<template><a :href="href" :download="filename"><slot /></a></template>

