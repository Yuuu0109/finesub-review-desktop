<script setup lang="ts">
defineProps<{ lines: string[] }>();
</script>
<template>
  <div class="diff">
    <p v-if="!lines.length" class="empty-small">
      此版本与上一版本没有正文差异。
    </p>
    <div
      v-for="(line, index) in lines"
      :key="index"
      class="diff-line"
      :class="
        line.startsWith('+++') || line.startsWith('---')
          ? 'diff-heading'
          : line.startsWith('+')
            ? 'diff-add'
            : line.startsWith('-')
              ? 'diff-remove'
              : line.startsWith('@@')
                ? 'diff-heading'
                : ''
      "
    >
      <span class="line-number">{{ index + 1 }}</span
      ><code>{{ line || " " }}</code>
    </div>
  </div>
</template>
