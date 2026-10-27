<script setup lang="ts">
defineProps<{ name: string; size?: number }>();
const paths: Record<string, string> = {
  book: "M4 4h6a3 3 0 0 1 2 1 3 3 0 0 1 2-1h6v15h-6a3 3 0 0 0-2 1 3 3 0 0 0-2-1H4z M12 5v15",
  grid: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  check: "m5 12 4 4L19 6",
  search: "m21 21-5-5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0",
  upload: "M12 16V3 m-5 5 5-5 5 5 M4 15v5h16v-5",
  download: "M12 3v13 m-5-5 5 5 5-5 M4 17v4h16v-4",
  users:
    "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M13 3a4 4 0 0 1 0 8 M22 21v-2a4 4 0 0 0-4-4 M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  clock: "M12 8v5l3 2 M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
  message:
    "M21 15a3 3 0 0 1-3 3H8l-5 3V5a3 3 0 0 1 3-3h12a3 3 0 0 1 3 3z M7 7h10 M7 11h7",
  arrow: "M5 12h14 m-5-5 5 5-5 5",
  chevron: "m9 5 7 7-7 7",
  back: "m15 5-7 7 7 7",
  bell: "M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9 M10 21h4",
  settings: "M4 7h16 M4 17h16 M9 4v6 M15 14v6",
  logout: "M9 3H3v18h6 M9 12h12 m-4-4 4 4-4 4",
  plus: "M12 5v14 M5 12h14",
  close: "m6 6 12 12 M6 18 18 6",
  file: "M14 2H4v20h16V8z M14 2v6h6 M8 13h8 M8 17h6",
  shield: "m12 2 9 4v6c0 5-9 10-9 10S3 17 3 12V6z m-4 10 3 3 5-6",
  refresh:
    "M20 7v5h-5 M4 17v-5h5 M5 7a8 8 0 0 1 14-2l1 2 M4 17l1 2a8 8 0 0 0 14-2",
  edit: "m16 3 5 5-12 12H4v-5z M13 6l5 5",
  menu: "M4 6h16 M4 12h16 M4 18h16",
  link: "M10 13a5 5 0 0 0 7 .2l3-3a5 5 0 0 0-7-7l-2 2 M14 11a5 5 0 0 0-7-.2l-3 3a5 5 0 0 0 7 7l2-2",
  key: "M15 3a6 6 0 1 1-4.4 10.1L3 21v-4l3-3h3l1.9-1.9A6 6 0 0 1 15 3 M16 7h.01",
};
</script>
<template>
  <svg
    :width="size || 20"
    :height="size || 20"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name] || paths.file" />
  </svg>
</template>
