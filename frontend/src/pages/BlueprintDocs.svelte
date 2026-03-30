<script lang="ts">
  import { marked } from 'marked';
  import { navigate } from '$lib/router';
  import { Button } from '$lib/components/ui/button';
  import rawDoc from '$lib/docs/blueprint-reference.md?raw';

  const html = marked(rawDoc) as string;
</script>

<div class="max-w-4xl mx-auto space-y-6">
  <div class="flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold">YAML Blueprint Reference</h1>
      <p class="text-sm text-muted-foreground mt-1">
        Complete reference for writing Go-templated Pulumi YAML blueprints.
      </p>
    </div>
    <Button variant="outline" size="sm" onclick={() => navigate('/blueprints')}>
      ← Back to Blueprints
    </Button>
  </div>

  <div class="rounded-lg border bg-card p-8 prose-doc">
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    {@html html}
  </div>
</div>

<style>
  :global(.prose-doc) {
    color: hsl(var(--foreground));
    line-height: 1.7;
    font-size: 0.9rem;
  }

  :global(.prose-doc h1) {
    font-size: 1.75rem;
    font-weight: 700;
    margin-top: 0;
    margin-bottom: 0.75rem;
    padding-bottom: 0.5rem;
    border-bottom: 1px solid hsl(var(--border));
  }

  :global(.prose-doc h2) {
    font-size: 1.25rem;
    font-weight: 600;
    margin-top: 2.5rem;
    margin-bottom: 0.75rem;
    padding-bottom: 0.4rem;
    border-bottom: 1px solid hsl(var(--border));
  }

  :global(.prose-doc h3) {
    font-size: 1rem;
    font-weight: 600;
    margin-top: 1.75rem;
    margin-bottom: 0.5rem;
  }

  :global(.prose-doc p) {
    margin-bottom: 0.875rem;
  }

  :global(.prose-doc a) {
    color: hsl(var(--primary));
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  :global(.prose-doc strong) {
    font-weight: 600;
  }

  :global(.prose-doc ul),
  :global(.prose-doc ol) {
    padding-left: 1.5rem;
    margin-bottom: 0.875rem;
  }

  :global(.prose-doc ul) {
    list-style-type: disc;
  }

  :global(.prose-doc ol) {
    list-style-type: decimal;
  }

  :global(.prose-doc li) {
    margin-bottom: 0.25rem;
  }

  :global(.prose-doc pre) {
    background: hsl(var(--muted));
    border: 1px solid hsl(var(--border));
    border-radius: calc(var(--radius) - 2px);
    padding: 1rem 1.25rem;
    overflow-x: auto;
    margin: 0.875rem 0 1.25rem;
    font-size: 0.8rem;
    line-height: 1.6;
  }

  :global(.prose-doc code) {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.8em;
    background: hsl(var(--muted));
    border: 1px solid hsl(var(--border));
    border-radius: 3px;
    padding: 0.15em 0.35em;
  }

  :global(.prose-doc pre code) {
    background: none;
    border: none;
    padding: 0;
    font-size: inherit;
  }

  :global(.prose-doc table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
    margin: 0.875rem 0 1.25rem;
  }

  :global(.prose-doc th) {
    background: hsl(var(--muted));
    font-weight: 600;
    text-align: left;
    padding: 0.5rem 0.75rem;
    border: 1px solid hsl(var(--border));
    white-space: nowrap;
  }

  :global(.prose-doc td) {
    padding: 0.45rem 0.75rem;
    border: 1px solid hsl(var(--border));
    vertical-align: top;
  }

  :global(.prose-doc tr:nth-child(even)) {
    background: hsl(var(--muted) / 0.4);
  }

  :global(.prose-doc blockquote) {
    border-left: 3px solid hsl(var(--border));
    padding-left: 1rem;
    color: hsl(var(--muted-foreground));
    font-style: italic;
    margin: 0.875rem 0;
  }

  :global(.prose-doc hr) {
    border: none;
    border-top: 1px solid hsl(var(--border));
    margin: 2rem 0;
  }

  :global(.prose-doc dt) {
    font-weight: 600;
  }

  :global(.prose-doc dd) {
    margin-left: 1.5rem;
    margin-bottom: 0.5rem;
    color: hsl(var(--muted-foreground));
  }
</style>
