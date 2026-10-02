import { describe, expect, it } from 'vitest';
import { draftFromRequest, duplicateRepeaterTab, findResponseMatches, newRepeaterTab, requestFromDraft, tabLabel } from './repeaterTabs';

describe('Repeater tabs', () => {
  it('keeps editable header text while converting a draft to a request', () => {
    const request = { method: 'POST', url: 'https://example.test/path', headers: { 'X-Test': ['one', 'two'] }, body: 'hello' };
    const draft = draftFromRequest(request);
    expect(requestFromDraft(draft)).toEqual(request);
    expect(newRepeaterTab(2, request).result).toBeNull();
    expect(tabLabel(newRepeaterTab(2, request))).toBe('2: POST example.test');
  });

  it('does not treat an incomplete URL as a different tab', () => {
    expect(tabLabel(newRepeaterTab(3, { method: 'GET', url: 'https://', headers: {}, body: '' }))).toBe('3: GET New request');
  });

  it('duplicates raw draft text without copying response state', () => {
    const source = newRepeaterTab(1, { method: 'GET', url: 'https://example.test', headers: {}, body: '' });
    source.draft.headersText = 'X-Draft: unfinished\ninvalid header';
    source.pending = true;
    const copy = duplicateRepeaterTab(2, source);
    expect(copy.draft.headersText).toBe(source.draft.headersText);
    expect(copy.draft).not.toBe(source.draft);
    expect(copy.pending).toBe(false);
    expect(copy.result).toBeNull();
  });

  it('finds case-insensitive, non-overlapping response matches', () => {
    expect(findResponseMatches('Alpha beta ALPHA', 'alpha')).toEqual([0, 11]);
    expect(findResponseMatches('aaaa', 'aa')).toEqual([0, 2]);
    expect(findResponseMatches('text', '')).toEqual([]);
    expect(findResponseMatches('İ alpha İ', 'İ')).toEqual([0, 8]);
    expect(findResponseMatches('a.b a-b', 'a.b')).toEqual([0]);
  });
});
