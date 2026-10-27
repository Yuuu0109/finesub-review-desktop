<script setup lang="ts">
import { onMounted, ref, useId } from "vue";
import Icon from "./Icon.vue";
defineProps<{ title: string; wide?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const dialog = ref<HTMLDialogElement>();
const titleId = useId();
onMounted(() => dialog.value?.showModal());
</script>
<template>
  <dialog
    ref="dialog"
    class="modal"
    :aria-labelledby="titleId"
    :class="{ wide }"
    @cancel.prevent="emit('close')"
    @click="
      (e) => {
        if (e.target === dialog) emit('close');
      }
    "
  >
    <div class="modal-header">
      <h2 :id="titleId">{{ title }}</h2>
      <button class="icon-button" aria-label="关闭弹窗" @click="emit('close')">
        <Icon name="close" />
      </button>
    </div>
    <div class="modal-body"><slot /></div>
  </dialog>
</template>
