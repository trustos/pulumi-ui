<script lang="ts">
  let {
    size = 32,
    class: className = '',
  }: { size?: number; class?: string } = $props();
</script>

<!--
  Three stacked bars of decreasing width — represents infrastructure layers /
  resource stacks. Indigo background keeps it consistent with the favicon.
-->
<svg
  width={size}
  height={size}
  viewBox="0 0 32 32"
  fill="none"
  xmlns="http://www.w3.org/2000/svg"
  class={className}
  aria-hidden="true"
>
  <!-- Background -->
  <rect width="32" height="32" rx="8" fill="#4f46e5" />

  <!-- Top bar — full width, white -->
  <rect x="6" y="7" width="20" height="5" rx="2.5" fill="white" />

  <!-- Middle bar — 3/4 width, slightly faded -->
  <rect x="6" y="14" width="14" height="5" rx="2.5" fill="white" fill-opacity="0.65" />

  <!-- Bottom bar — half width, most faded -->
  <rect x="6" y="21" width="9" height="5" rx="2.5" fill="white" fill-opacity="0.35" />
</svg>
