import type { BlueprintGraph, BlueprintItem } from '$lib/types/blueprint-graph';

/**
 * Renames a resource across the entire BlueprintGraph:
 * - Property values: ${oldName.xxx} → ${newName.xxx}, ${oldName} → ${newName}
 * - dependsOn arrays: oldName → newName
 * - Output values: ${oldName.xxx} → ${newName.xxx}
 *
 * Does NOT rename the resource itself (caller handles that via bind).
 * Returns a new graph if any references changed, or the same graph if nothing matched.
 */
export function propagateRename(graph: BlueprintGraph, oldName: string, newName: string): BlueprintGraph {
  if (!oldName || !newName || oldName === newName) return graph;

  const refPattern = new RegExp(`\\$\\{${escapeRegex(oldName)}([.\\[}])`, 'g');
  const exactPattern = new RegExp(`^\\$\\{${escapeRegex(oldName)}\\}$`);

  let changed = false;

  function replaceInValue(value: string): string {
    let result = value;
    if (exactPattern.test(result)) {
      result = result.replace(exactPattern, `\${${newName}}`);
    }
    if (refPattern.test(result)) {
      refPattern.lastIndex = 0;
      result = result.replace(refPattern, `\${${newName}$1`);
    }
    if (result !== value) changed = true;
    return result;
  }

  function processItems(items: BlueprintItem[]): BlueprintItem[] {
    return items.map(item => {
      if (item.kind === 'resource') {
        const newProps = item.properties.map(p => {
          const newVal = replaceInValue(p.value);
          return newVal !== p.value ? { ...p, value: newVal } : p;
        });

        let newDeps = item.options?.dependsOn;
        if (newDeps?.includes(oldName)) {
          newDeps = newDeps.map(d => d === oldName ? newName : d);
          changed = true;
        }

        const propsChanged = newProps.some((p, i) => p !== item.properties[i]);
        const depsChanged = newDeps !== item.options?.dependsOn;

        if (!propsChanged && !depsChanged) return item;
        return {
          ...item,
          properties: propsChanged ? newProps : item.properties,
          options: depsChanged ? { ...item.options, dependsOn: newDeps } : item.options,
        };
      } else if (item.kind === 'loop') {
        const newItems = processItems(item.items);
        return newItems !== item.items ? { ...item, items: newItems } : item;
      } else if (item.kind === 'conditional') {
        const newItems = processItems(item.items);
        const newElse = item.elseItems ? processItems(item.elseItems) : item.elseItems;
        if (newItems === item.items && newElse === item.elseItems) return item;
        return { ...item, items: newItems, elseItems: newElse };
      }
      return item;
    });
  }

  const newSections = graph.sections.map(s => {
    const newItems = processItems(s.items);
    return newItems !== s.items ? { ...s, items: newItems } : s;
  });

  const newOutputs = graph.outputs.map(o => {
    const newVal = replaceInValue(o.value);
    return newVal !== o.value ? { ...o, value: newVal } : o;
  });

  if (!changed) return graph;

  return {
    ...graph,
    sections: newSections,
    outputs: newOutputs,
  };
}

/**
 * Renames a resource in YAML text using regex replacement.
 * Replaces ${oldName.xxx}, ${oldName}, and dependsOn references.
 */
export function propagateRenameYaml(yamlText: string, oldName: string, newName: string): string {
  if (!oldName || !newName || oldName === newName) return yamlText;

  const escaped = escapeRegex(oldName);
  let result = yamlText;

  // ${oldName.property} and ${oldName[index]}
  result = result.replace(
    new RegExp(`\\$\\{${escaped}([.\\[])`, 'g'),
    `\${${newName}$1`,
  );

  // ${oldName} (exact, e.g. in dependsOn)
  result = result.replace(
    new RegExp(`\\$\\{${escaped}\\}`, 'g'),
    `\${${newName}}`,
  );

  // dependsOn list items: "- oldName" (as a YAML resource reference without ${})
  // Not needed — Pulumi YAML dependsOn uses ${name} syntax

  return result;
}

function escapeRegex(str: string): string {
  return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
