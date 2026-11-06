<script setup lang="ts">
import Brand from "./Brand.vue";
import Icon from "./Icon.vue";
</script>

<template>
  <aside class="welcome-panel">
    <Brand />
    <div class="welcome-art" aria-hidden="true">
      <div class="welcome-paper">
        <Icon name="book" :size="32" />
        <i></i><i></i><i></i><i></i>
        <span><Icon name="check" :size="26" /></span>
      </div>
    </div>
    <span class="welcome-footer">FineSub Review</span>
  </aside>
</template>
