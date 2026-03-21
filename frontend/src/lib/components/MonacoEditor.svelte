<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { ValidationError } from '$lib/types';
  import { getOciSchema, getResourceTypes } from '$lib/schema';

  let {
    value = $bindable(''),
    height = '400px',
    readonly = false,
    markers = [] as ValidationError[],
    onchange,
  }: {
    value?: string;
    height?: string;
    readonly?: boolean;
    markers?: ValidationError[];
    onchange?: () => void;
  } = $props();

  let container: HTMLDivElement;
  let editor: any;
  let monaco: any;

  onMount(async () => {
    const monacoModule = await import('monaco-editor');
    monaco = monacoModule;

    // Register yaml-gotemplate language if not already registered
    const existingLangs = monaco.languages.getLanguages();
    const hasYamlGotemplate = existingLangs.some((l: any) => l.id === 'yaml-gotemplate');
    if (!hasYamlGotemplate) {
      monaco.languages.register({ id: 'yaml-gotemplate' });
      monaco.languages.setMonarchTokensProvider('yaml-gotemplate', {
        tokenizer: {
          root: [
            [/\{\{.*?\}\}/, 'keyword'],
            [/^\s*#.*$/, 'comment'],
            [/:\s*$/, 'operator'],
            [/"[^"]*"/, 'string'],
            [/'[^']*'/, 'string'],
            [/\$\{[^}]+\}/, 'variable'],
            [/\b(true|false|null)\b/, 'keyword.constant'],
            [/\b\d+(\.\d+)?\b/, 'number'],
          ],
        },
      });
    }

    editor = monaco.editor.create(container, {
      value,
      language: 'yaml-gotemplate',
      theme: window.matchMedia('(prefers-color-scheme: dark)').matches ? 'vs-dark' : 'vs',
      readOnly: readonly,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontSize: 13,
      lineNumbers: 'on',
      wordWrap: 'on',
      automaticLayout: true,
      tabSize: 2,
      insertSpaces: true,
    });

    editor.onDidChangeModelContent(() => {
      const v = editor.getValue();
      if (v !== value) {
        value = v;
        onchange?.();
      }
    });

    // OCI resource type autocomplete
    getOciSchema().then(schema => {
      const resourceTypes = getResourceTypes(schema);
      monaco.languages.registerCompletionItemProvider('yaml-gotemplate', {
        triggerCharacters: [' '],
        provideCompletionItems(model: any, position: any) {
          const linePrefix = model.getLineContent(position.lineNumber).slice(0, position.column - 1);
          if (!/type:\s*\S*$/.test(linePrefix)) return { suggestions: [] };
          const word = model.getWordUntilPosition(position);
          const range = {
            startLineNumber: position.lineNumber,
            endLineNumber: position.lineNumber,
            startColumn: word.startColumn,
            endColumn: position.column,
          };
          return {
            suggestions: resourceTypes.map(t => ({
              label: t,
              kind: monaco.languages.CompletionItemKind.Class,
              insertText: t,
              range,
              detail: schema.resources[t]?.description ?? '',
            })),
          };
        },
      });
    }).catch(() => {/* schema unavailable — no autocomplete */});
  });

  onDestroy(() => {
    editor?.dispose();
  });

  // Sync external value changes into the editor
  $effect(() => {
    if (editor && value !== editor.getValue()) {
      const pos = editor.getPosition();
      editor.setValue(value);
      if (pos) editor.setPosition(pos);
    }
  });

  // Sync markers into the editor
  $effect(() => {
    if (!editor || !monaco) return;
    const model = editor.getModel();
    if (!model) return;
    const monacoMarkers = markers.map(err => ({
      severity: err.level <= 2
        ? monaco.MarkerSeverity.Error
        : monaco.MarkerSeverity.Warning,
      startLineNumber: err.line ?? 1,
      startColumn: 1,
      endLineNumber: err.line ?? 1,
      endColumn: 200,
      message: err.message,
    }));
    monaco.editor.setModelMarkers(model, 'validation', monacoMarkers);
  });

  // Sync readonly prop
  $effect(() => {
    editor?.updateOptions({ readOnly: readonly });
  });
</script>

<div bind:this={container} style="height: {height}; width: 100%;"></div>
