import { describe, it, expect } from 'vitest';
import {
  parseObjectValue,
  serializeObjectValue,
  parseArrayValue,
  serializeArrayValue,
} from './object-value';

describe('parseObjectValue', () => {
  it('parses a simple object', () => {
    expect(parseObjectValue('{ key: "val" }')).toEqual({ key: 'val' });
  });

  it('parses multiple key-value pairs', () => {
    expect(parseObjectValue('{ a: "1", b: "2", c: "3" }')).toEqual({
      a: '1',
      b: '2',
      c: '3',
    });
  });

  it('parses resource references', () => {
    expect(parseObjectValue('{ subnetId: "${subnet.id}" }')).toEqual({
      subnetId: '${subnet.id}',
    });
  });

  it('parses config references', () => {
    expect(parseObjectValue('{ imageId: "{{ .Config.imageId }}" }')).toEqual({
      imageId: '{{ .Config.imageId }}',
    });
  });

  it('parses unquoted booleans and numbers', () => {
    expect(parseObjectValue('{ enabled: true, port: 80 }')).toEqual({
      enabled: 'true',
      port: '80',
    });
  });

  it('parses nested objects', () => {
    const input = '{ destinationPortRange: { min: 80, max: 80 } }';
    const result = parseObjectValue(input);
    expect(result).toEqual({ destinationPortRange: '{ min: 80, max: 80 }' });
  });

  it('parses template expressions without quotes', () => {
    expect(parseObjectValue('{ ocpus: {{ .Config.ocpus }} }')).toEqual({
      ocpus: '{{ .Config.ocpus }}',
    });
  });

  it('returns empty for empty string', () => {
    expect(parseObjectValue('')).toEqual({});
  });

  it('returns empty for non-object string', () => {
    expect(parseObjectValue('not an object')).toEqual({});
  });

  it('returns empty for malformed input', () => {
    expect(parseObjectValue('{ broken')).toEqual({});
  });

  it('returns empty for empty braces', () => {
    expect(parseObjectValue('{}')).toEqual({});
  });

  it('handles values containing commas inside quotes', () => {
    expect(parseObjectValue('{ desc: "a, b", name: "c" }')).toEqual({
      desc: 'a, b',
      name: 'c',
    });
  });

  it('handles values containing colons in URLs', () => {
    expect(parseObjectValue('{ proto: "http://example.com" }')).toEqual({
      proto: 'http://example.com',
    });
  });

  it('handles mixed quoted and unquoted values', () => {
    expect(
      parseObjectValue('{ sourceType: "image", imageId: "${img.id}", size: 50 }')
    ).toEqual({ sourceType: 'image', imageId: '${img.id}', size: '50' });
  });
});

describe('serializeObjectValue', () => {
  it('serializes a simple object', () => {
    expect(serializeObjectValue({ key: 'val' })).toBe('{ key: "val" }');
  });

  it('serializes boolean and number values unquoted', () => {
    expect(serializeObjectValue({ enabled: 'true', port: '80' })).toBe(
      '{ enabled: true, port: 80 }'
    );
  });

  it('preserves resource references quoted', () => {
    expect(serializeObjectValue({ subnetId: '${subnet.id}' })).toBe(
      '{ subnetId: "${subnet.id}" }'
    );
  });

  it('preserves template expressions unquoted', () => {
    expect(serializeObjectValue({ ocpus: '{{ .Config.ocpus }}' })).toBe(
      '{ ocpus: {{ .Config.ocpus }} }'
    );
  });

  it('serializes empty fields as empty object', () => {
    expect(serializeObjectValue({})).toBe('{}');
  });

  it('skips empty-string values', () => {
    expect(serializeObjectValue({ a: 'val', b: '' })).toBe('{ a: "val" }');
  });

  it('preserves nested object values', () => {
    expect(
      serializeObjectValue({ portRange: '{ min: 80, max: 80 }' })
    ).toBe('{ portRange: { min: 80, max: 80 } }');
  });
});

describe('parseArrayValue', () => {
  it('parses an array of objects', () => {
    const input = '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]';
    const result = parseArrayValue(input);
    expect(result).toEqual([
      { destination: '0.0.0.0/0', networkEntityId: '${igw.id}' },
    ]);
  });

  it('parses multi-item arrays', () => {
    const input =
      '[{ destination: "0.0.0.0/0", target: "${igw.id}" }, { destination: "10.0.0.0/8", target: "${nat.id}" }]';
    const result = parseArrayValue(input);
    expect(result).toHaveLength(2);
    expect(result[0]).toEqual({ destination: '0.0.0.0/0', target: '${igw.id}' });
    expect(result[1]).toEqual({ destination: '10.0.0.0/8', target: '${nat.id}' });
  });

  it('returns empty for non-array string', () => {
    expect(parseArrayValue('not an array')).toEqual([]);
  });

  it('returns empty for empty array', () => {
    expect(parseArrayValue('[]')).toEqual([]);
  });

  it('returns empty for malformed elements', () => {
    expect(parseArrayValue('[broken]')).toEqual([]);
  });
});

describe('serializeArrayValue', () => {
  it('serializes an array of objects', () => {
    const items = [{ destination: '0.0.0.0/0', networkEntityId: '${igw.id}' }];
    const result = serializeArrayValue(items);
    expect(result).toBe(
      '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]'
    );
  });

  it('serializes multiple items', () => {
    const items = [
      { a: 'x' },
      { a: 'y' },
    ];
    expect(serializeArrayValue(items)).toBe('[{ a: "x" }, { a: "y" }]');
  });

  it('serializes empty array', () => {
    expect(serializeArrayValue([])).toBe('[]');
  });
});

describe('round-trip', () => {
  it('parse then serialize produces equivalent output for simple object', () => {
    const original = '{ sourceType: "image", imageId: "${img.id}" }';
    const parsed = parseObjectValue(original);
    const serialized = serializeObjectValue(parsed);
    expect(serialized).toBe(original);
  });

  it('parse then serialize for array', () => {
    const original =
      '[{ destination: "0.0.0.0/0", networkEntityId: "${igw.id}" }]';
    const parsed = parseArrayValue(original);
    const serialized = serializeArrayValue(parsed);
    expect(serialized).toBe(original);
  });

  it('parse then serialize for booleans', () => {
    const original = '{ assignPublicIp: true }';
    const parsed = parseObjectValue(original);
    const serialized = serializeObjectValue(parsed);
    expect(serialized).toBe(original);
  });
});
