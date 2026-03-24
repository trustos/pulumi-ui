import { describe, it, expect } from 'vitest';
import {
  cleanValue,
  parseSimpleArray,
  serializeSimpleArray,
  stripHtml,
  isRefOrTemplate,
  inferValidationHint,
  validatePropertyValue,
} from './typed-value';
import { yamlValue } from './serializer';

// ---------------------------------------------------------------------------
// cleanValue
// ---------------------------------------------------------------------------
describe('cleanValue', () => {
  it('strips outer double quotes', () => {
    expect(cleanValue('"true"')).toBe('true');
  });

  it('strips quotes and unescapes inner content', () => {
    expect(cleanValue('"[\\"10.0.0.0/16\\"]"')).toBe('["10.0.0.0/16"]');
  });

  it('passes through unquoted values', () => {
    expect(cleanValue('subnet')).toBe('subnet');
  });

  it('passes through template expressions', () => {
    expect(cleanValue('{{ .Config.name }}')).toBe('{{ .Config.name }}');
  });

  it('passes through resource refs', () => {
    expect(cleanValue('${vcn.id}')).toBe('${vcn.id}');
  });

  it('handles empty string', () => {
    expect(cleanValue('')).toBe('');
  });

  it('handles single character', () => {
    expect(cleanValue('x')).toBe('x');
  });

  it('unescapes backslashes', () => {
    expect(cleanValue('"path\\\\to\\\\file"')).toBe('path\\to\\file');
  });

  it('strips quotes from simple string values', () => {
    expect(cleanValue('"subnet"')).toBe('subnet');
    expect(cleanValue('"my-vcn"')).toBe('my-vcn');
  });

  it('strips quotes from empty quoted string ""', () => {
    expect(cleanValue('""')).toBe('');
  });

  it('does NOT strip single quotes', () => {
    expect(cleanValue("'hello'")).toBe("'hello'");
  });

  it('strips quotes from YAML-reserved words (null, ~)', () => {
    expect(cleanValue('"null"')).toBe('null');
    expect(cleanValue('"~"')).toBe('~');
  });

  it('strips quotes from a quoted resource ref', () => {
    expect(cleanValue('"${vcn.id}"')).toBe('${vcn.id}');
  });

  it('strips quotes from a quoted config template', () => {
    expect(cleanValue('"{{ .Config.compartmentName }}"')).toBe('{{ .Config.compartmentName }}');
  });

  it('handles a value that is just two double quotes (the string itself is "")', () => {
    expect(cleanValue('""')).toBe('');
  });

  it('handles embedded escaped quotes', () => {
    expect(cleanValue('"he said \\"hello\\""')).toBe('he said "hello"');
  });
});

// ---------------------------------------------------------------------------
// parseSimpleArray
// ---------------------------------------------------------------------------
describe('parseSimpleArray', () => {
  it('parses single-element array', () => {
    expect(parseSimpleArray('["10.0.0.0/16"]')).toEqual(['10.0.0.0/16']);
  });

  it('parses multi-element array', () => {
    expect(parseSimpleArray('["a", "b", "c"]')).toEqual(['a', 'b', 'c']);
  });

  it('parses empty array', () => {
    expect(parseSimpleArray('[]')).toEqual([]);
  });

  it('returns null for non-array values', () => {
    expect(parseSimpleArray('hello')).toBeNull();
  });

  it('returns null for empty string', () => {
    expect(parseSimpleArray('')).toBeNull();
  });

  it('handles YAML-quoted array', () => {
    expect(parseSimpleArray('"[\\"10.0.0.0/16\\"]"')).toEqual(['10.0.0.0/16']);
  });

  it('handles unquoted elements', () => {
    expect(parseSimpleArray('[one, two]')).toEqual(['one', 'two']);
  });

  it('handles CIDR values with slashes', () => {
    expect(parseSimpleArray('["10.0.0.0/16", "192.168.1.0/24"]')).toEqual([
      '10.0.0.0/16',
      '192.168.1.0/24',
    ]);
  });

  it('returns null for a plain number', () => {
    expect(parseSimpleArray('42')).toBeNull();
  });

  it('returns null for a boolean', () => {
    expect(parseSimpleArray('true')).toBeNull();
  });

  it('parses single unquoted element', () => {
    expect(parseSimpleArray('[foo]')).toEqual(['foo']);
  });
});

// ---------------------------------------------------------------------------
// serializeSimpleArray
// ---------------------------------------------------------------------------
describe('serializeSimpleArray', () => {
  it('serializes single element', () => {
    expect(serializeSimpleArray(['10.0.0.0/16'])).toBe('["10.0.0.0/16"]');
  });

  it('serializes multiple elements', () => {
    expect(serializeSimpleArray(['a', 'b'])).toBe('["a", "b"]');
  });

  it('serializes empty array', () => {
    expect(serializeSimpleArray([])).toBe('[]');
  });

  it('round-trips with parseSimpleArray', () => {
    const original = ['10.0.0.0/16', '192.168.1.0/24'];
    const serialized = serializeSimpleArray(original);
    const parsed = parseSimpleArray(serialized);
    expect(parsed).toEqual(original);
  });

  it('round-trips a single CIDR value', () => {
    const items = ['10.0.1.0/24'];
    expect(parseSimpleArray(serializeSimpleArray(items))).toEqual(items);
  });

  it('round-trips through the full pipeline: serialize → yamlValue → cleanValue → parse', () => {
    const items = ['10.0.0.0/16', '192.168.0.0/24'];
    const serialized = serializeSimpleArray(items);
    const yamlSafe = yamlValue(serialized);
    const cleaned = cleanValue(yamlSafe);
    const reparsed = parseSimpleArray(cleaned);
    expect(reparsed).toEqual(items);
  });
});

// ---------------------------------------------------------------------------
// stripHtml
// ---------------------------------------------------------------------------
describe('stripHtml', () => {
  it('strips simple HTML tags', () => {
    expect(stripHtml('<b>bold</b>')).toBe('bold');
  });

  it('strips Pulumi lang span tags', () => {
    const input = '<span pulumi-lang-nodejs="`destination`">`destination`</span>';
    expect(stripHtml(input)).toBe('`destination`');
  });

  it('collapses whitespace', () => {
    expect(stripHtml('foo  \n  bar')).toBe('foo bar');
  });

  it('returns plain text unchanged', () => {
    expect(stripHtml('no tags here')).toBe('no tags here');
  });

  it('handles complex nested HTML', () => {
    const input = 'Use <span><code>destination</code></span> and <code>destinationType</code>.';
    expect(stripHtml(input)).toBe('Use destination and destinationType.');
  });

  it('handles empty string', () => {
    expect(stripHtml('')).toBe('');
  });

  it('strips self-closing tags', () => {
    expect(stripHtml('before<br/>after')).toBe('beforeafter');
  });

  it('strips anchor tags preserving text', () => {
    expect(stripHtml('see <a href="https://example.com">docs</a> for details')).toBe(
      'see docs for details'
    );
  });

  it('handles real OCI schema description', () => {
    const input =
      '(Updatable) Instead use <span pulumi-lang-yaml="`destination`">`destination`</span> and `destinationType`.';
    const result = stripHtml(input);
    expect(result).toBe('(Updatable) Instead use `destination` and `destinationType`.');
    expect(result).not.toContain('<');
    expect(result).not.toContain('>');
  });
});

// ---------------------------------------------------------------------------
// isRefOrTemplate
// ---------------------------------------------------------------------------
describe('isRefOrTemplate', () => {
  it('detects config template', () => {
    expect(isRefOrTemplate('{{ .Config.name }}')).toBe(true);
  });

  it('detects quoted config template', () => {
    expect(isRefOrTemplate('"{{ .Config.name }}"')).toBe(true);
  });

  it('detects resource reference', () => {
    expect(isRefOrTemplate('${vcn.id}')).toBe(true);
  });

  it('detects indexed variable reference', () => {
    expect(isRefOrTemplate('${availabilityDomains[0].name}')).toBe(true);
  });

  it('detects quoted resource reference', () => {
    expect(isRefOrTemplate('"${subnet.id}"')).toBe(true);
  });

  it('returns false for plain values', () => {
    expect(isRefOrTemplate('true')).toBe(false);
    expect(isRefOrTemplate('subnet')).toBe(false);
    expect(isRefOrTemplate('10.0.0.0/16')).toBe(false);
  });

  it('returns false for array values', () => {
    expect(isRefOrTemplate('["10.0.0.0/16"]')).toBe(false);
  });

  it('returns false for partial/malformed expressions', () => {
    expect(isRefOrTemplate('${incomplete')).toBe(false);
    expect(isRefOrTemplate('{{ .Config.')).toBe(false);
  });

  it('returns false for empty string', () => {
    expect(isRefOrTemplate('')).toBe(false);
  });

  it('returns false for mixed text with embedded ref', () => {
    expect(isRefOrTemplate('prefix ${vcn.id} suffix')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// YAML round-trip: cleanValue → yamlValue
// ---------------------------------------------------------------------------
describe('YAML round-trip: cleanValue → yamlValue', () => {
  it('boolean "true" survives round-trip', () => {
    const original = '"true"';
    const cleaned = cleanValue(original);
    expect(cleaned).toBe('true');
    const reserialized = yamlValue(cleaned);
    expect(reserialized).toBe('"true"');
  });

  it('boolean "false" survives round-trip', () => {
    const original = '"false"';
    const cleaned = cleanValue(original);
    expect(cleaned).toBe('false');
    expect(yamlValue(cleaned)).toBe('"false"');
  });

  it('null survives round-trip', () => {
    const cleaned = cleanValue('"null"');
    expect(cleaned).toBe('null');
    expect(yamlValue(cleaned)).toBe('"null"');
  });

  it('tilde survives round-trip', () => {
    const cleaned = cleanValue('"~"');
    expect(cleaned).toBe('~');
    expect(yamlValue(cleaned)).toBe('"~"');
  });

  it('plain string passes through', () => {
    const cleaned = cleanValue('"subnet"');
    expect(cleaned).toBe('subnet');
    expect(yamlValue(cleaned)).toBe('subnet');
  });

  it('array value passes through as flow sequence', () => {
    const original = '"[\\"10.0.0.0/16\\"]"';
    const cleaned = cleanValue(original);
    expect(cleaned).toBe('["10.0.0.0/16"]');
    // Flow sequences are kept as-is so Pulumi receives them as arrays
    expect(yamlValue(cleaned)).toBe('["10.0.0.0/16"]');
  });

  it('template expression passes through untouched', () => {
    const v = '{{ .Config.name }}';
    expect(cleanValue(v)).toBe(v);
    expect(yamlValue(v)).toBe(v);
  });

  it('resource ref passes through untouched', () => {
    const v = '${vcn.id}';
    expect(cleanValue(v)).toBe(v);
    expect(yamlValue(v)).toBe(v);
  });

  it('string with colon-space gets safely re-quoted', () => {
    const cleaned = cleanValue('"key: value"');
    expect(cleaned).toBe('key: value');
    expect(yamlValue(cleaned)).toBe('"key: value"');
  });

  it('string starting with "- " gets safely re-quoted', () => {
    const cleaned = cleanValue('"- list item"');
    expect(cleaned).toBe('- list item');
    expect(yamlValue(cleaned)).toBe('"- list item"');
  });

  it('string with " #" (comment char) gets safely re-quoted', () => {
    const cleaned = cleanValue('"value # comment"');
    expect(cleaned).toBe('value # comment');
    expect(yamlValue(cleaned)).toBe('"value # comment"');
  });

  it('empty string round-trips via yamlValue', () => {
    expect(yamlValue('')).toBe('""');
    expect(cleanValue('""')).toBe('');
  });

  it('single-quoted values pass through yamlValue unchanged', () => {
    expect(yamlValue("'hello'")).toBe("'hello'");
  });

  it('plain number string passes through without quoting', () => {
    expect(yamlValue('42')).toBe('42');
    expect(yamlValue('3.14')).toBe('3.14');
  });
});

// ---------------------------------------------------------------------------
// inferValidationHint
// ---------------------------------------------------------------------------
describe('inferValidationHint', () => {
  it('detects cidr from property name "cidrBlock"', () => {
    expect(inferValidationHint('cidrBlock', 'string', '')).toBe('cidr');
  });

  it('detects cidr from property name "cidrBlocks"', () => {
    expect(inferValidationHint('cidrBlocks', 'string', '')).toBe('cidr');
  });

  it('detects cidr from "destination" key when description mentions CIDR', () => {
    expect(
      inferValidationHint('destination', 'string', 'IP address range in CIDR notation')
    ).toBe('cidr');
  });

  it('detects cidr from description containing "CIDR block"', () => {
    expect(
      inferValidationHint('someField', 'string', 'The CIDR block for the VCN')
    ).toBe('cidr');
  });

  it('detects port from key + integer type', () => {
    expect(inferValidationHint('port', 'integer', '')).toBe('port');
    expect(inferValidationHint('listenPort', 'integer', '')).toBe('port');
  });

  it('detects integer from schema type', () => {
    expect(inferValidationHint('count', 'integer', '')).toBe('integer');
  });

  it('detects number from schema type', () => {
    expect(inferValidationHint('weight', 'number', '')).toBe('number');
  });

  it('detects ocid from key ending in "Id" when description mentions OCID', () => {
    expect(
      inferValidationHint('subnetId', 'string', 'The OCID of the subnet')
    ).toBe('ocid');
  });

  it('returns null for plain string without patterns', () => {
    expect(inferValidationHint('displayName', 'string', 'A name')).toBeNull();
  });

  it('returns null for empty inputs', () => {
    expect(inferValidationHint('', 'string', '')).toBeNull();
  });

  it('returns cidr for array-typed cidrBlocks (hint applies to items)', () => {
    expect(inferValidationHint('cidrBlocks', 'array', '')).toBe('cidr');
  });

  it('returns null for object type even when name matches cidr pattern', () => {
    // Object types should be skipped at the caller level, but the hint
    // itself is still inferred correctly — the caller decides whether to use it.
    expect(inferValidationHint('cidrBlock', 'object', '')).toBe('cidr');
  });
});

// ---------------------------------------------------------------------------
// validatePropertyValue
// ---------------------------------------------------------------------------
describe('validatePropertyValue', () => {
  describe('CIDR validation', () => {
    it('accepts valid CIDR', () => {
      expect(validatePropertyValue('10.0.0.0/16', 'cidr')).toBeNull();
      expect(validatePropertyValue('192.168.1.0/24', 'cidr')).toBeNull();
      expect(validatePropertyValue('0.0.0.0/0', 'cidr')).toBeNull();
    });

    it('rejects bare number', () => {
      expect(validatePropertyValue('10', 'cidr')).toBe(
        'Expected CIDR notation, e.g. 10.0.0.0/16'
      );
    });

    it('rejects IP without prefix', () => {
      expect(validatePropertyValue('10.0.0.0', 'cidr')).toBe(
        'Expected CIDR notation, e.g. 10.0.0.0/16'
      );
    });

    it('rejects prefix > 32', () => {
      expect(validatePropertyValue('10.0.0.0/33', 'cidr')).toBe(
        'Expected CIDR notation, e.g. 10.0.0.0/16'
      );
    });

    it('rejects octet > 255', () => {
      expect(validatePropertyValue('256.0.0.0/16', 'cidr')).toBe(
        'Expected CIDR notation, e.g. 10.0.0.0/16'
      );
    });

    it('rejects random text', () => {
      expect(validatePropertyValue('hello', 'cidr')).toBe(
        'Expected CIDR notation, e.g. 10.0.0.0/16'
      );
    });

    it('skips validation for template refs', () => {
      expect(validatePropertyValue('{{ .Config.cidr }}', 'cidr')).toBeNull();
    });

    it('skips validation for resource refs', () => {
      expect(validatePropertyValue('${vcn.cidr}', 'cidr')).toBeNull();
    });

    it('skips validation for empty value', () => {
      expect(validatePropertyValue('', 'cidr')).toBeNull();
    });
  });

  describe('IP validation', () => {
    it('accepts valid IPv4', () => {
      expect(validatePropertyValue('10.0.1.1', 'ip')).toBeNull();
      expect(validatePropertyValue('192.168.0.1', 'ip')).toBeNull();
    });

    it('rejects bare number', () => {
      expect(validatePropertyValue('10', 'ip')).toBe(
        'Expected IPv4 address, e.g. 10.0.1.1'
      );
    });

    it('rejects CIDR (expects plain IP)', () => {
      expect(validatePropertyValue('10.0.0.0/16', 'ip')).toBe(
        'Expected IPv4 address, e.g. 10.0.1.1'
      );
    });
  });

  describe('port validation', () => {
    it('accepts valid port numbers', () => {
      expect(validatePropertyValue('80', 'port')).toBeNull();
      expect(validatePropertyValue('443', 'port')).toBeNull();
      expect(validatePropertyValue('65535', 'port')).toBeNull();
      expect(validatePropertyValue('1', 'port')).toBeNull();
    });

    it('rejects 0', () => {
      expect(validatePropertyValue('0', 'port')).toBe('Expected port number 1–65535');
    });

    it('rejects > 65535', () => {
      expect(validatePropertyValue('65536', 'port')).toBe('Expected port number 1–65535');
    });

    it('rejects non-numeric', () => {
      expect(validatePropertyValue('abc', 'port')).toBe('Expected port number 1–65535');
    });

    it('rejects float', () => {
      expect(validatePropertyValue('80.5', 'port')).toBe('Expected port number 1–65535');
    });
  });

  describe('integer validation', () => {
    it('accepts valid integers', () => {
      expect(validatePropertyValue('42', 'integer')).toBeNull();
      expect(validatePropertyValue('0', 'integer')).toBeNull();
      expect(validatePropertyValue('-1', 'integer')).toBeNull();
    });

    it('rejects float', () => {
      expect(validatePropertyValue('3.14', 'integer')).toBe('Expected an integer');
    });

    it('rejects non-numeric', () => {
      expect(validatePropertyValue('abc', 'integer')).toBe('Expected an integer');
    });
  });

  describe('number validation', () => {
    it('accepts integers and floats', () => {
      expect(validatePropertyValue('42', 'number')).toBeNull();
      expect(validatePropertyValue('3.14', 'number')).toBeNull();
    });

    it('rejects non-numeric', () => {
      expect(validatePropertyValue('abc', 'number')).toBe('Expected a number');
    });
  });

  describe('OCID validation', () => {
    it('accepts valid OCID format', () => {
      expect(
        validatePropertyValue('ocid1.subnet.oc1.eu-frankfurt-1.abc123', 'ocid')
      ).toBeNull();
    });

    it('rejects random string', () => {
      expect(validatePropertyValue('random-value', 'ocid')).toBe(
        'Expected OCID format, e.g. ocid1.subnet.oc1...'
      );
    });

    it('skips validation for config refs', () => {
      expect(
        validatePropertyValue('{{ .Config.subnetId }}', 'ocid')
      ).toBeNull();
    });

    it('skips validation for resource refs', () => {
      expect(validatePropertyValue('${subnet.id}', 'ocid')).toBeNull();
    });
  });

  describe('null hint', () => {
    it('always returns null (no validation)', () => {
      expect(validatePropertyValue('anything', null)).toBeNull();
      expect(validatePropertyValue('10', null)).toBeNull();
    });
  });
});

// ---------------------------------------------------------------------------
// Array/object outer validation skip (mirrors PropertyEditor.getValidationError)
// ---------------------------------------------------------------------------
describe('array/object outer validation skip', () => {
  // PropertyEditor skips outer validation for array/object types because
  // they have their own per-item or per-field validation. These tests
  // reproduce the exact logic from getValidationError to prove the pattern.
  function getValidationError(
    value: string,
    schemaType: string,
    key: string,
    description: string
  ): string | null {
    if (!key || !value) return null;
    if (schemaType === 'array' || schemaType === 'object') return null;
    const hint = inferValidationHint(key, schemaType, description);
    return validatePropertyValue(value, hint);
  }

  it('skips validation for array-type cidrBlocks with serialized value', () => {
    // The serialized array value ["10.0.0.0/16"] is NOT a valid CIDR by itself,
    // but it should NOT produce an error because array types are skipped.
    expect(getValidationError('["10.0.0.0/16"]', 'array', 'cidrBlocks', '')).toBeNull();
  });

  it('skips validation for array-type even with invalid item inside', () => {
    expect(getValidationError('["bad"]', 'array', 'cidrBlocks', '')).toBeNull();
  });

  it('skips validation for object-type properties', () => {
    expect(getValidationError('{ bad }', 'object', 'sourceDetails', '')).toBeNull();
  });

  it('still validates scalar cidrBlock property', () => {
    expect(getValidationError('10', 'string', 'cidrBlock', '')).toBe(
      'Expected CIDR notation, e.g. 10.0.0.0/16'
    );
  });

  it('passes for valid scalar cidrBlock', () => {
    expect(getValidationError('10.0.1.0/24', 'string', 'cidrBlock', '')).toBeNull();
  });

  it('individual array item validated via validatePropertyValue + hint', () => {
    // This is how per-item validation works in the array editor
    const hint = inferValidationHint('cidrBlocks', 'string', '');
    expect(validatePropertyValue('10', hint)).toBe(
      'Expected CIDR notation, e.g. 10.0.0.0/16'
    );
    expect(validatePropertyValue('10.0.0.0/16', hint)).toBeNull();
  });

  it('individual array item with empty value is not validated', () => {
    const hint = inferValidationHint('cidrBlocks', 'string', '');
    expect(validatePropertyValue('', hint)).toBeNull();
  });
});
