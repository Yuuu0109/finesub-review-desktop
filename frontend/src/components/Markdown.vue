<script setup lang="ts">
import { computed } from "vue";
import MarkdownIt from "markdown-it";
import DOMPurify from "dompurify";
const props = defineProps<{ text: string }>();
const md = new MarkdownIt({ html: false, linkify: true, breaks: true });
md.disable("image");
const content = computed(() =>
  DOMPurify.sanitize(md.render(props.text), { USE_PROFILES: { html: true } }),
);
</script>
<template><div class="markdown" v-html="content"></div></template>
