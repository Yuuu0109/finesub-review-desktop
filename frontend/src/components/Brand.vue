<script setup lang="ts">
// 图标是矢量书页标记：缩到 44px 仍然清楚，位图插图在这个尺寸会糊。
import mark from "../assets/icons/source.svg";
</script>

<template>
  <span class="brand">
    <img class="brand-art" :src="mark" alt="" width="44" height="44" />
    <span>FineSub<small>知识库协作</small></span>
  </span>
</template>
