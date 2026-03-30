<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { ValidationError } from '$lib/types';
  import { getOciSchema, getResourceTypes } from '$lib/schema';
  import { propagateRenameYaml } from '$lib/blueprint-graph/rename-resource';

  let {
    value = $bindable(''),
    height = '400px',
    readonly = false,
    markers = [] as ValidationError[],
    onchange,
    enableResourceRename = false,
  }: {
    value?: string;
    height?: string;
    readonly?: boolean;
    markers?: ValidationError[];
    onchange?: () => void;
    enableResourceRename?: boolean;
  } = $props();

  let container: HTMLDivElement;
  let editor: any;
  let monaco: any;
  let isInternalChange = false;

  onMount(async () => {
    const loaderModule = await import('@monaco-editor/loader');
    const loader = loaderModule.default;
    monaco = await loader.init();

    // Register yaml-gotemplate language (YAML + Go template colouring)
    if (!monaco.languages.getLanguages().some((l: any) => l.id === 'yaml-gotemplate')) {
      monaco.languages.register({ id: 'yaml-gotemplate' });
      monaco.languages.setMonarchTokensProvider('yaml-gotemplate', {
        tokenizer: {
          root: [
            [/\{\{.*?\}\}/, 'keyword'],
            [/^\s*#.*$/, 'comment'],
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
      fontSize: 12,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      lineNumbers: 'on',
      wordWrap: 'on',
      automaticLayout: true,
      tabSize: 2,
      insertSpaces: true,
    });

    editor.onDidChangeModelContent(() => {
      if (isInternalChange) return;
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
          if (!/type:\s*\S*/.test(linePrefix)) return { suggestions: [] };
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

    if (enableResourceRename) {
      editor.addAction({
        id: 'rename-resource',
        label: 'Rename Resource (update all references)',
        keybindings: [monaco.KeyCode.F2],
        contextMenuGroupId: 'navigation',
        contextMenuOrder: 1,
        run(ed: any) {
          const position = ed.getPosition();
          if (!position) return;
          const word = ed.getModel()?.getWordAtPosition(position);
          if (!word) return;
          const oldName = word.word;
          const newName = prompt(`Rename resource "${oldName}" to:`, oldName);
          if (!newName || newName === oldName) return;
          const updated = propagateRenameYaml(ed.getValue(), oldName, newName);
          if (updated !== ed.getValue()) {
            ed.setValue(updated);
            onchange?.();
          }
        },
      });
    }
  });

  onDestroy(() => {
    editor?.dispose();
  });

  // Sync external value changes into the editor (e.g. file import)
  $effect(() => {
    if (!editor) return;
    const current = editor.getValue();
    if (current !== value) {
      isInternalChange = true;
      editor.setValue(value);
      isInternalChange = false;
    }
  });

  // Sync markers → Monaco squiggles
  $effect(() => {
    if (!editor || !monaco) return;
    const model = editor.getModel();
    if (!model) return;
    const monacoMarkers = (markers ?? [])
      .filter(e => e.line != null)
      .map(e => ({
        severity: monaco.MarkerSeverity.Error,
        message: (e.field ? `[${e.field}] ` : '') + e.message,
        startLineNumber: e.line!,
        startColumn: 1,
        endLineNumber: e.line!,
        endColumn: model.getLineMaxColumn(e.line!),
      }));
    monaco.editor.setModelMarkers(model, 'validate', monacoMarkers);
  });

  $effect(() => {
    editor?.updateOptions({ readOnly: readonly });
  });
</script>

<div
  bind:this={container}
  style="height: {height}; width: 100%;"
  class="rounded-md border overflow-hidden"
></div>
